// Copyright (C) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package workflow

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/shubhamdubey02/apm/storage"
)

var _ Workflow = AddRepository{}

func NewAddRepository(config AddRepositoryConfig) *AddRepository {
	return &AddRepository{
		sourcesList: config.SourcesList,
		alias:       config.Alias,
		url:         config.URL,
		branch:      config.Branch,
	}
}

type AddRepositoryConfig struct {
	SourcesList storage.Storage[storage.SourceInfo]
	Alias, URL  string
	Branch      plumbing.ReferenceName
}

type AddRepository struct {
	sourcesList storage.Storage[storage.SourceInfo]
	alias, url  string
	branch      plumbing.ReferenceName
}

func (a AddRepository) Execute() error {
	aliasBytes := []byte(a.alias)

	if ok, err := a.sourcesList.Has(aliasBytes); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("%s is already registered as a repository", a.alias)
	}

	unsynced := storage.SourceInfo{
		Alias:  a.alias,
		URL:    a.url,
		Branch: a.branch,
		Commit: plumbing.ZeroHash, // hasn't been synced yet
	}
	return a.sourcesList.Put(aliasBytes, unsynced)
}
