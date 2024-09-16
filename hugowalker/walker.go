package hugowalker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/afero"

	"github.com/gohugoio/hugo/config"
	"github.com/gohugoio/hugo/config/allconfig"
	"github.com/gohugoio/hugo/hugolib"
	"github.com/gohugoio/hugo/resources/page"
	"github.com/gohugoio/hugo/resources/resource"

	"github.com/gohugoio/hugo/deps"
	"github.com/gohugoio/hugo/hugofs"
)

func New(dir string) (hw *Walker, err error) {
	// If fully qualified, use as is, else join the path with the current working directory.
	if !filepath.IsAbs(dir) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
		dir = filepath.Join(wd, dir)
	}
	// Hugo is _obsessed_ with running from the working directory.
	if err = os.Chdir(dir); err != nil {
		return nil, fmt.Errorf("failed to change working directory to %q: %w", dir, err)
	}

	fs := afero.NewOsFs()
	configs, err := allconfig.LoadConfig(
		allconfig.ConfigSourceDescriptor{
			Fs:       fs,
			Filename: "hugo.toml",
		})
	if err != nil {
		return nil, fmt.Errorf("failed to load Hugo configuration: %w", err)
	}
	hfs := hugofs.NewFrom(fs, config.BaseConfig{
		WorkingDir: dir,
		CacheDir:   configs.Base.CacheDir,
		ThemesDir:  configs.Base.ThemesDir,
		PublishDir: configs.Base.PublishDir,
	})
	sites, err := hugolib.NewHugoSites(deps.DepsCfg{
		Fs:      hfs,
		Configs: configs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load Hugo site(s): %w", err)
	}
	if err = sites.Build(hugolib.BuildCfg{SkipRender: true}); err != nil {
		return nil, fmt.Errorf("failed to build Hugo site(s): %w", err)
	}
	hw = &Walker{
		sites: sites,
	}
	return hw, nil
}

type Walker struct {
	sites *hugolib.HugoSites
}

func (hw *Walker) Walk(yield func(page.Page) bool) {
	for _, p := range hw.sites.Pages() {
		if p.Draft() || resource.IsFuture(p) || resource.IsExpired(p) {
			continue
		}
		if !yield(p) {
			return
		}
	}
}
