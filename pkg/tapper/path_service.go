package tapper

import (
	"context"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/appctx"
)

type PathService struct {
	*appctx.AppContext
}

func NewPathService(ctx context.Context, root string) (*PathService, error) {
	appS, err := appctx.NewAppContext(ctx, root, DefaultAppName)
	if err != nil {
		return nil, err
	}

	service := PathService{appS}
	return &service, nil
}

func (s *PathService) Project() string {
	return s.AppContext.LocalConfigRoot
}

func (s *PathService) ProjectConfig() string {
	return filepath.Join(s.LocalConfigRoot, "config.yaml")
}

func (s *PathService) UserConfig() string {
	return filepath.Join(s.ConfigRoot, "config.yaml")
}
