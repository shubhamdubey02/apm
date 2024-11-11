// Copyright (C) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package apm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/MetalBlockchain/metalgo/database"
	"github.com/MetalBlockchain/metalgo/database/leveldb"
	"github.com/MetalBlockchain/metalgo/utils/logging"
	"github.com/MetalBlockchain/metalgo/utils/perms"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/afero"

	"github.com/shubhamdubey02/apm/admin"
	"github.com/shubhamdubey02/apm/constant"
	"github.com/shubhamdubey02/apm/engine"
	"github.com/shubhamdubey02/apm/git"
	"github.com/shubhamdubey02/apm/storage"
	"github.com/shubhamdubey02/apm/types"
	"github.com/shubhamdubey02/apm/url"
	"github.com/shubhamdubey02/apm/util"
	"github.com/shubhamdubey02/apm/workflow"
)

var (
	dbDir            = "db"
	repositoryDir    = "repositories"
	tmpDir           = "tmp"
	metricsNamespace = "apm_db"
)

type Config struct {
	Directory        string
	Auth             http.BasicAuth
	AdminAPIEndpoint string
	PluginDir        string
	Fs               afero.Fs
}

type APM struct {
	db database.Database

	sourcesList  storage.Storage[storage.SourceInfo]
	installedVMs storage.Storage[storage.InstallInfo]
	registry     storage.Storage[storage.RepoList]
	repoFactory  storage.RepositoryFactory

	executor workflow.Executor

	auth http.BasicAuth

	adminClient admin.Client
	installer   workflow.Installer

	repositoriesPath string
	tmpPath          string
	pluginPath       string
	adminAPIEndpoint string
	fs               afero.Fs
}

func New(config Config) (*APM, error) {
	dbDir := filepath.Join(config.Directory, dbDir)
	db, err := leveldb.New(dbDir, []byte{}, logging.NoLog{}, metricsNamespace, prometheus.NewRegistry())
	if err != nil {
		return nil, err
	}

	a := &APM{
		repositoriesPath: filepath.Join(config.Directory, repositoryDir),
		tmpPath:          filepath.Join(config.Directory, tmpDir),
		pluginPath:       config.PluginDir,
		db:               db,
		registry:         storage.NewRegistry(db),
		sourcesList:      storage.NewSourceInfo(db),
		installedVMs:     storage.NewInstalledVMs(db),
		auth:             config.Auth,
		adminAPIEndpoint: config.AdminAPIEndpoint,
		adminClient:      admin.NewClient(fmt.Sprintf("http://%s", config.AdminAPIEndpoint)),
		installer: workflow.NewVMInstaller(
			workflow.VMInstallerConfig{
				Fs:        config.Fs,
				URLClient: url.NewClient(),
			},
		),
		executor:    engine.NewWorkflowEngine(),
		fs:          config.Fs,
		repoFactory: storage.NewRepositoryFactory(db),
	}
	if err := os.MkdirAll(a.repositoriesPath, perms.ReadWriteExecute); err != nil {
		return nil, err
	}

	// TODO simplify this
	coreKey := []byte(constant.CoreAlias)
	if ok, err := a.sourcesList.Has(coreKey); err != nil {
		return nil, err
	} else if !ok {
		err := a.AddRepository(constant.CoreAlias, constant.CoreURL, constant.CoreBranch)
		if err != nil {
			return nil, err
		}
	}

	repoMetadata, err := a.sourcesList.Get(coreKey)
	if err != nil {
		return nil, err
	}

	if repoMetadata.Commit == plumbing.ZeroHash {
		fmt.Println("Bootstrap not detected. Bootstrapping...")
		err := a.Update()
		if err != nil {
			return nil, err
		}

		fmt.Println("Finished bootstrapping.")
	}
	return a, nil
}

func parseAndRun(alias string, registry storage.Storage[storage.RepoList], command func(string) error) error {
	if qualifiedName(alias) {
		return command(alias)
	}

	fullName, err := getFullNameForAlias(registry, alias)
	if err != nil {
		return err
	}

	return command(fullName)
}

func (a *APM) Install(alias string) error {
	return parseAndRun(alias, a.registry, a.install)
}

func (a *APM) install(name string) error {
	nameBytes := []byte(name)

	ok, err := a.installedVMs.Has(nameBytes)
	if err != nil {
		return err
	}

	if ok {
		fmt.Printf("VM %s is already installed. Skipping.\n", name)
		return nil
	}

	repoAlias, plugin := util.ParseQualifiedName(name)
	organization, repo := util.ParseAlias(repoAlias)

	repository := a.repoFactory.GetRepository([]byte(repoAlias))

	workflow := workflow.NewInstall(workflow.InstallConfig{
		Name:         name,
		Plugin:       plugin,
		Organization: organization,
		Repo:         repo,
		TmpPath:      a.tmpPath,
		PluginPath:   a.pluginPath,
		InstalledVMs: a.installedVMs,
		VMStorage:    repository.VMs,
		Fs:           a.fs,
		Installer:    a.installer,
	})

	return a.executor.Execute(workflow)
}

func (a *APM) Uninstall(alias string) error {
	return parseAndRun(alias, a.registry, a.uninstall)
}

func (a *APM) uninstall(name string) error {
	alias, plugin := util.ParseQualifiedName(name)

	repository := a.repoFactory.GetRepository([]byte(alias))

	wf := workflow.NewUninstall(
		workflow.UninstallConfig{
			Name:         name,
			Plugin:       plugin,
			RepoAlias:    alias,
			VMStorage:    repository.VMs,
			InstalledVMs: a.installedVMs,
			Fs:           a.fs,
			PluginPath:   a.pluginPath,
		},
	)

	return wf.Execute()
}

func (a *APM) JoinSubnet(alias string) error {
	return parseAndRun(alias, a.registry, a.joinSubnet)
}

func (a *APM) joinSubnet(fullName string) error {
	alias, plugin := util.ParseQualifiedName(fullName)
	repoRegistry := a.repoFactory.GetRepository([]byte(alias))

	var (
		definition storage.Definition[types.Subnet]
		err        error
	)

	definition, err = repoRegistry.Subnets.Get([]byte(plugin))
	if err != nil {
		return err
	}

	subnet := definition.Definition

	// TODO prompt user, add force flag
	fmt.Printf("Installing virtual machines for subnet %s.\n", subnet.GetID())
	for _, vm := range subnet.VMs {
		if err := a.Install(strings.Join([]string{alias, vm}, constant.QualifiedNameDelimiter)); err != nil {
			return err
		}
	}

	fmt.Printf("Updating virtual machines...\n")
	if err := a.adminClient.LoadVMs(); errors.Is(err, syscall.ECONNREFUSED) {
		fmt.Printf("Node at %s was offline. Virtual machines will be available upon node startup.\n", a.adminAPIEndpoint)
	} else if err != nil {
		return err
	}

	fmt.Printf("Whitelisting subnet %s...\n", subnet.GetID())
	if err := a.adminClient.WhitelistSubnet(subnet.GetID()); errors.Is(err, syscall.ECONNREFUSED) {
		fmt.Printf("Node at %s was offline. You'll need to whitelist the subnet upon node restart.\n", a.adminAPIEndpoint)
	} else if err != nil {
		return err
	}

	fmt.Printf("Finished installing virtual machines for subnet %s.\n", subnet.ID)
	return nil
}

func (a *APM) Info(alias string) error {
	if qualifiedName(alias) {
		return a.install(alias)
	}

	fullName, err := getFullNameForAlias(a.registry, alias)
	if err != nil {
		return err
	}

	return a.info(fullName)
}

func (a *APM) info(fullName string) error {
	return nil
}

func (a *APM) Update() error {
	workflow := workflow.NewUpdate(workflow.UpdateConfig{
		Executor:         a.executor,
		Registry:         a.registry,
		InstalledVMs:     a.installedVMs,
		SourcesList:      a.sourcesList,
		DB:               a.db,
		TmpPath:          a.tmpPath,
		PluginPath:       a.pluginPath,
		Installer:        a.installer,
		RepositoriesPath: a.repositoriesPath,
		Auth:             a.auth,
		GitFactory:       git.RepositoryFactory{},
		RepoFactory:      storage.NewRepositoryFactory(a.db),
		Fs:               a.fs,
	})

	if err := a.executor.Execute(workflow); err != nil {
		return err
	}

	return nil
}

func (a *APM) Upgrade(alias string) error {
	// If we have an alias specified, upgrade the specified VM.
	if alias != "" {
		return parseAndRun(alias, a.registry, a.upgradeVM)
	}

	// Otherwise, just upgrade everything.
	wf := workflow.NewUpgrade(workflow.UpgradeConfig{
		Executor:     a.executor,
		RepoFactory:  a.repoFactory,
		Registry:     a.registry,
		SourcesList:  a.sourcesList,
		InstalledVMs: a.installedVMs,
		TmpPath:      a.tmpPath,
		PluginPath:   a.pluginPath,
		Installer:    a.installer,
		Fs:           a.fs,
	})

	return a.executor.Execute(wf)
}

func (a *APM) upgradeVM(name string) error {
	return a.executor.Execute(workflow.NewUpgradeVM(
		workflow.UpgradeVMConfig{
			Executor:     a.executor,
			FullVMName:   name,
			RepoFactory:  a.repoFactory,
			InstalledVMs: a.installedVMs,
			TmpPath:      a.tmpPath,
			PluginPath:   a.pluginPath,
			Installer:    a.installer,
			Fs:           a.fs,
		},
	))
}

func (a *APM) AddRepository(alias string, url string, branch string) error {
	if !util.ValidAlias(alias) {
		return fmt.Errorf("%s is not a valid alias (must be in the form of organization/repository)", alias)
	}

	wf := workflow.NewAddRepository(
		workflow.AddRepositoryConfig{
			SourcesList: a.sourcesList,
			Alias:       alias,
			URL:         url,
			Branch:      plumbing.NewBranchReferenceName(branch),
		},
	)

	return a.executor.Execute(wf)
}

func (a *APM) RemoveRepository(alias string) error {
	if alias == constant.CoreAlias {
		fmt.Printf("Can't remove %s (required repository).\n", constant.CoreAlias)
		return nil
	}

	aliasBytes := []byte(alias)
	repoRegistry := a.repoFactory.GetRepository(aliasBytes)

	// delete all the plugin definitions in the repository
	vmItr := repoRegistry.VMs.Iterator()
	defer vmItr.Release()

	for vmItr.Next() {
		if err := repoRegistry.VMs.Delete(vmItr.Key()); err != nil {
			return err
		}
	}

	subnetItr := repoRegistry.Subnets.Iterator()
	defer subnetItr.Release()

	for subnetItr.Next() {
		if err := repoRegistry.Subnets.Delete(subnetItr.Key()); err != nil {
			return err
		}
	}

	// remove it from our list of tracked repositories
	return a.sourcesList.Delete(aliasBytes)
}

func (a *APM) ListRepositories() error {
	itr := a.sourcesList.Iterator()

	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
	fmt.Fprintln(w, "alias\turl\tbranch")
	for itr.Next() {
		metadata, err := itr.Value()
		if err != nil {
			return err
		}

		fmt.Fprintf(w, "%s\t%s\t%s\n", metadata.Alias, metadata.URL, metadata.Branch)
	}
	w.Flush()
	return nil
}

func qualifiedName(name string) bool {
	parsed := strings.Split(name, ":")
	return len(parsed) > 1
}

func getFullNameForAlias(registry storage.Storage[storage.RepoList], alias string) (string, error) {
	repoList, err := registry.Get([]byte(alias))
	if err != nil {
		return "", err
	}

	if len(repoList.Repositories) > 1 {
		return "", fmt.Errorf("more than one match found for %s. Please specify the fully qualified name. Matches: %s", alias, repoList.Repositories)
	}

	return fmt.Sprintf("%s:%s", repoList.Repositories[0], alias), nil
}
