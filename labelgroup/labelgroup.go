package labelgroup

import (
	"context"
	"golang.org/x/sync/errgroup"
	"runtime/pprof"
)

type Group struct {
	*errgroup.Group
}

func WithContext(ctx context.Context) (*Group, context.Context) {
	g, ctx := errgroup.WithContext(ctx)
	return &Group{Group: g}, ctx
}

func (g *Group) Go(ctx context.Context, labels pprof.LabelSet, f func(ctx context.Context) error) {
	pprof.Do(ctx, labels, func(ctx context.Context) {
		g.Group.Go(func() error {
			return f(ctx)
		})
	})
}
