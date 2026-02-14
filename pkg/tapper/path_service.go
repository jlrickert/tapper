package tapper

import (
	"path/filepath"

	appctx "github.com/jlrickert/cli-toolkit/apppaths"
	"github.com/jlrickert/cli-toolkit/toolkit"
)

type PathService struct {
	*appctx.AppPaths
}

func NewPathService(rt *toolkit.Runtime, root string) (*PathService, error) {
	appS, err := appctx.NewAppPaths(rt, root, DefaultAppName)
	if err != nil {
		return nil, err
	}

	service := PathService{appS}
	return &service, nil
}

func (s *PathService) Project() string {
	return s.AppPaths.LocalConfigRoot
}

func (s *PathService) ProjectConfig() string {
	return filepath.Join(s.LocalConfigRoot, "config.yaml")
}

func (s *PathService) UserConfig() string {
	return filepath.Join(s.ConfigRoot, "config.yaml")
}
