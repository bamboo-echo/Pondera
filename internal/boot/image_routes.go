package boot

import (
	"context"
	"fmt"
	"pondera/internal/attachment"
	"pondera/internal/config"
	"pondera/internal/control"
	"pondera/internal/imageinput"
	"pondera/internal/provider"
)

type childImageRouting struct {
	controller func() *control.Controller
	config     *imageinput.Config
}

type controllerImageResolver struct{ controller func() *control.Controller }

func (r controllerImageResolver) ResolveRequestImages(ctx context.Context, messages []provider.Message) ([]provider.Message, error) {
	if c := r.controller(); c != nil {
		return c.ResolveRequestImages(ctx, messages)
	}
	return nil, fmt.Errorf("attachment owner is unavailable")
}

func (r controllerImageResolver) ResolveRequestImagesForModel(ctx context.Context, messages []provider.Message, model string, native bool) ([]provider.Message, error) {
	if c := r.controller(); c != nil {
		return c.ResolveRequestImagesForModel(ctx, messages, model, native)
	}
	return nil, fmt.Errorf("attachment owner is unavailable")
}

func (r controllerImageResolver) PersistToolImages(ctx context.Context, images []string) ([]attachment.ImageInput, error) {
	if c := r.controller(); c != nil {
		return c.PersistToolImages(ctx, images)
	}
	return nil, fmt.Errorf("attachment owner is unavailable")
}

func newControllerWithImageRoutes(opts control.Options, cfg *config.Config) *control.Controller {
	opts.ImageRouteConfig = cfg
	return control.New(opts)
}
