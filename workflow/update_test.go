// Copyright (C) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package workflow

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/golang/mock/gomock"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"

	"github.com/shubhamdubey02/apm/git"
	"github.com/shubhamdubey02/apm/storage"
	mockdb "github.com/shubhamdubey02/apm/storage/mocks"
)

func TestUpdateExecute(t *testing.T) {
	const (
		organization     = "organization"
		repo             = "repository"
		alias            = "organization/repository"
		url              = "url"
		tmpPath          = "tmpPath"
		pluginPath       = "pluginPath"
		repositoriesPath = "repositoriesPath"
	)

	var (
		errWrong = fmt.Errorf("something went wrong")

		previousCommit  = plumbing.Hash{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
		latestCommit    = plumbing.Hash{2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2}
		repoInstallPath = filepath.Join(repositoriesPath, organization, repo)
		repository      = storage.Repository{}

		auth = http.BasicAuth{
			Username: "username",
			Password: "password",
		}

		branch = plumbing.NewBranchReferenceName("branch")

		sourceInfo = storage.SourceInfo{
			Alias:  alias,
			URL:    url,
			Branch: branch,
			Commit: previousCommit,
		}

		fs = afero.NewMemMapFs()
	)

	sourceInfoBytes, err := yaml.Marshal(sourceInfo)
	if err != nil {
		t.Fatal(err)
	}

	garbageBytes := []byte("garbage")

	type mocks struct {
		ctrl         *gomock.Controller
		executor     *MockExecutor
		registry     *storage.MockStorage[storage.RepoList]
		installedVMs *storage.MockStorage[storage.InstallInfo]
		sourcesList  *storage.MockStorage[storage.SourceInfo]
		db           *mockdb.MockDatabase
		installer    *MockInstaller
		gitFactory   *git.MockFactory
		repoFactory  *storage.MockRepositoryFactory
		auth         http.BasicAuth
	}
	tests := []struct {
		name    string
		setup   func(mocks)
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "bad source info",
			setup: func(mocks mocks) {
				// iterator with only one key/value pair
				mocks.sourcesList.EXPECT().Iterator().DoAndReturn(func() storage.Iterator[storage.SourceInfo] {
					itr := mockdb.NewMockIterator(mocks.ctrl)
					defer itr.EXPECT().Release()

					itr.EXPECT().Next().Return(true)
					itr.EXPECT().Key().Return([]byte(alias))

					itr.EXPECT().Value().Return(garbageBytes)

					return *storage.NewIterator[storage.SourceInfo](itr)
				})
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.Error(t, err)
			},
		},
		{
			name: "cant get latest git head",
			setup: func(mocks mocks) {
				// iterator with only one key/value pair
				mocks.sourcesList.EXPECT().Iterator().DoAndReturn(func() storage.Iterator[storage.SourceInfo] {
					itr := mockdb.NewMockIterator(mocks.ctrl)
					defer itr.EXPECT().Release()

					itr.EXPECT().Next().Return(true)
					itr.EXPECT().Key().Return([]byte(alias))

					itr.EXPECT().Value().Return(sourceInfoBytes)

					return *storage.NewIterator[storage.SourceInfo](itr)
				})

				mocks.gitFactory.EXPECT().GetRepository(url, repoInstallPath, branch, &mocks.auth).Return(plumbing.ZeroHash, errWrong)
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.Equal(t, errWrong, err)
			},
		},
		{
			name: "workflow fails",
			setup: func(mocks mocks) {
				// iterator with only one key/value pair
				mocks.sourcesList.EXPECT().Iterator().DoAndReturn(func() storage.Iterator[storage.SourceInfo] {
					itr := mockdb.NewMockIterator(mocks.ctrl)
					defer itr.EXPECT().Release()

					itr.EXPECT().Next().Return(true)
					itr.EXPECT().Key().Return([]byte(alias))

					itr.EXPECT().Value().Return(sourceInfoBytes)

					return *storage.NewIterator[storage.SourceInfo](itr)
				})

				wf := NewUpdateRepository(UpdateRepositoryConfig{
					RepoName:       repo,
					RepositoryPath: repoInstallPath,
					AliasBytes:     []byte(alias),
					PreviousCommit: previousCommit,
					LatestCommit:   latestCommit,
					Repository:     repository,
					Registry:       mocks.registry,
					SourceInfo:     sourceInfo,
					SourcesList:    mocks.sourcesList,
					Fs:             fs,
				})

				mocks.gitFactory.EXPECT().GetRepository(url, repoInstallPath, branch, &mocks.auth).Return(latestCommit, nil)
				mocks.repoFactory.EXPECT().GetRepository([]byte(alias)).Return(repository)
				mocks.executor.EXPECT().Execute(wf).Return(errWrong)
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.Equal(t, errWrong, err)
			},
		},
		{
			name: "success single repository no upgrade needed",
			setup: func(mocks mocks) {
				// iterator with only one key/value pair
				mocks.sourcesList.EXPECT().Iterator().DoAndReturn(func() storage.Iterator[storage.SourceInfo] {
					itr := mockdb.NewMockIterator(mocks.ctrl)
					defer itr.EXPECT().Release()

					itr.EXPECT().Next().Return(true)
					itr.EXPECT().Key().Return([]byte(alias))

					itr.EXPECT().Value().Return(sourceInfoBytes)
					itr.EXPECT().Next().Return(false)

					return *storage.NewIterator[storage.SourceInfo](itr)
				})

				mocks.gitFactory.EXPECT().GetRepository(url, repoInstallPath, branch, &mocks.auth).Return(previousCommit, nil)
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.NoError(t, err)
			},
		},
		{
			name: "success single repository updates",
			setup: func(mocks mocks) {
				// iterator with only one key/value pair
				mocks.sourcesList.EXPECT().Iterator().DoAndReturn(func() storage.Iterator[storage.SourceInfo] {
					itr := mockdb.NewMockIterator(mocks.ctrl)
					defer itr.EXPECT().Release()

					itr.EXPECT().Next().Return(true)
					itr.EXPECT().Key().Return([]byte(alias))

					itr.EXPECT().Value().Return(sourceInfoBytes)
					itr.EXPECT().Next().Return(false)

					return *storage.NewIterator[storage.SourceInfo](itr)
				})

				wf := NewUpdateRepository(UpdateRepositoryConfig{
					RepoName:       repo,
					RepositoryPath: repoInstallPath,
					AliasBytes:     []byte(alias),
					PreviousCommit: previousCommit,
					LatestCommit:   latestCommit,
					Repository:     repository,
					Registry:       mocks.registry,
					SourceInfo:     sourceInfo,
					SourcesList:    mocks.sourcesList,
					Fs:             fs,
				})

				mocks.gitFactory.EXPECT().GetRepository(url, repoInstallPath, branch, &mocks.auth).Return(latestCommit, nil)
				mocks.repoFactory.EXPECT().GetRepository([]byte(alias)).Return(repository)
				mocks.executor.EXPECT().Execute(wf).Return(nil)
			},
			wantErr: func(t assert.TestingT, err error, i ...interface{}) bool {
				return assert.NoError(t, err)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			var registry *storage.MockStorage[storage.RepoList]
			var installedVMs *storage.MockStorage[storage.InstallInfo]
			var sourcesList *storage.MockStorage[storage.SourceInfo]

			executor := NewMockExecutor(ctrl)
			db := mockdb.NewMockDatabase(ctrl)
			installer := NewMockInstaller(ctrl)
			gitFactory := git.NewMockFactory(ctrl)
			repoFactory := storage.NewMockRepositoryFactory(ctrl)

			registry = storage.NewMockStorage[storage.RepoList](ctrl)
			installedVMs = storage.NewMockStorage[storage.InstallInfo](ctrl)
			sourcesList = storage.NewMockStorage[storage.SourceInfo](ctrl)

			test.setup(mocks{
				ctrl:         ctrl,
				executor:     executor,
				registry:     registry,
				installedVMs: installedVMs,
				sourcesList:  sourcesList,
				db:           db,
				installer:    installer,
				gitFactory:   gitFactory,
				auth:         auth,
				repoFactory:  repoFactory,
			})

			wf := NewUpdate(
				UpdateConfig{
					Executor:         executor,
					Registry:         registry,
					InstalledVMs:     installedVMs,
					SourcesList:      sourcesList,
					DB:               db,
					TmpPath:          tmpPath,
					PluginPath:       pluginPath,
					Installer:        installer,
					RepositoriesPath: repositoriesPath,
					Auth:             auth,
					GitFactory:       gitFactory,
					RepoFactory:      repoFactory,
					Fs:               fs,
				},
			)
			test.wantErr(t, wf.Execute())
		})
	}
}
