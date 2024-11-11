// Copyright (C) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package workflow

import (
	"fmt"
	"path/filepath"

	"github.com/MetalBlockchain/metalgo/database"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/spf13/afero"

	"github.com/shubhamdubey02/apm/git"
	"github.com/shubhamdubey02/apm/storage"
	"github.com/shubhamdubey02/apm/util"
)

var _ Workflow = &Update{}

type UpdateConfig struct {
	Executor         Executor
	Registry         storage.Storage[storage.RepoList]
	InstalledVMs     storage.Storage[storage.InstallInfo]
	SourcesList      storage.Storage[storage.SourceInfo]
	DB               database.Database
	TmpPath          string
	PluginPath       string
	Installer        Installer
	RepositoriesPath string
	Auth             http.BasicAuth
	GitFactory       git.Factory
	RepoFactory      storage.RepositoryFactory
	Fs               afero.Fs
}

func NewUpdate(config UpdateConfig) *Update {
	return &Update{
		executor:         config.Executor,
		registry:         config.Registry,
		installedVMs:     config.InstalledVMs,
		db:               config.DB,
		tmpPath:          config.TmpPath,
		pluginPath:       config.PluginPath,
		installer:        config.Installer,
		sourcesList:      config.SourcesList,
		repositoriesPath: config.RepositoriesPath,
		auth:             config.Auth,
		gitFactory:       config.GitFactory,
		repoFactory:      config.RepoFactory,
		fs:               config.Fs,
	}
}

type Update struct {
	executor         Executor
	db               database.Database
	registry         storage.Storage[storage.RepoList]
	installedVMs     storage.Storage[storage.InstallInfo]
	sourcesList      storage.Storage[storage.SourceInfo]
	installer        Installer
	auth             http.BasicAuth
	tmpPath          string
	pluginPath       string
	repositoriesPath string
	gitFactory       git.Factory
	repoFactory      storage.RepositoryFactory
	fs               afero.Fs
}

func (u Update) Execute() error {
	itr := u.sourcesList.Iterator()
	defer itr.Release()

	for itr.Next() {
		aliasBytes := itr.Key()
		alias := string(aliasBytes)
		organization, repo := util.ParseAlias(alias)

		sourceInfo, err := itr.Value()
		if err != nil {
			return err
		}
		previousCommit := sourceInfo.Commit
		repositoryPath := filepath.Join(u.repositoriesPath, organization, repo)
		latestCommit, err := u.gitFactory.GetRepository(sourceInfo.URL, repositoryPath, sourceInfo.Branch, &u.auth)
		if err != nil {
			return err
		}

		if latestCommit == previousCommit {
			fmt.Printf("Already at latest for %s@%s.\n", alias, latestCommit)
			continue
		}

		workflow := NewUpdateRepository(UpdateRepositoryConfig{
			RepoName:       repo,
			RepositoryPath: repositoryPath,
			AliasBytes:     aliasBytes,
			PreviousCommit: previousCommit,
			LatestCommit:   latestCommit,
			Repository:     u.repoFactory.GetRepository(aliasBytes),
			Registry:       u.registry,
			SourceInfo:     sourceInfo,
			SourcesList:    u.sourcesList,
			Fs:             u.fs,
		})

		if err := u.executor.Execute(workflow); err != nil {
			return err
		}
	}

	return nil
}
