package teststore

import (
	"context"

	"github.com/linli/im/server/internal/model"
)

// Memory is a minimal business-state fixture for isolated tests. It is kept
// outside the production store package and deliberately has no message engine.
type Memory struct{}

func (Memory) Load(context.Context) (*model.State, error) { return model.NewState(), nil }
func (Memory) Save(context.Context, *model.State) error   { return nil }
func (Memory) Ping(context.Context) error                 { return nil }
func (Memory) Close()                                     {}
