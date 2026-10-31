package wake

import (
	"sync"

	"github.com/ygelfand/libcountertop/pkg/inference/gemini"
	"github.com/ygelfand/libcountertop/pkg/inference/pryon"
	sharedwake "github.com/ygelfand/libcountertop/pkg/inference/wake"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/layout"
)

var (
	catalog = sync.OnceValue(func() *pryon.Catalog {
		return pryon.NewCatalog(layout.PryonDir, func() bool { return config.Get().Wake.Pryon })
	})
	google = sync.OnceValue(func() *gemini.Catalog {
		return gemini.NewCatalog(layout.GeminiDir, func() bool { return config.Get().Wake.Gemini })
	})
	get = sync.OnceValue(func() *sharedwake.Library {
		return sharedwake.NewLibrary(layout.ModelDir, catalog(), google())
	})
)

func Lib() *sharedwake.Library { return get() }

func Pryon() *pryon.Catalog { return catalog() }
