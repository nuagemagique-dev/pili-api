package db

import (
	"context"
	"fmt"

	"github.com/graph-gophers/dataloader/v7"
)

// TypedLoader is a generic dataloader wrapper that works with type T.
type TypedLoader[T any] struct {
	Loader *dataloader.Loader[string, []T]
}

// BatchFetchFuncTyped is a function type that fetches data for a batch of keys,
// returning a map from key to a slice of type T.
type BatchFetchFuncTyped[T any] func(ctx context.Context, keys []string) (map[string][]T, error)

// NewTypedLoader creates a new TypedLoader using the provided batch fetch function.
func NewTypedLoader[T any](batchFetchFunc BatchFetchFuncTyped[T], errorMessage string) *TypedLoader[T] {
	batchFn := func(ctx context.Context, ids []string) []*dataloader.Result[[]T] {
		dataMap, err := batchFetchFunc(ctx, ids)
		results := make([]*dataloader.Result[[]T], len(ids))
		if err != nil {
			for i := range ids {
				results[i] = &dataloader.Result[[]T]{
					Error: fmt.Errorf("%s: %w", errorMessage, err),
				}
			}
			return results
		}

		for i, id := range ids {
			if data, ok := dataMap[id]; ok {
				results[i] = &dataloader.Result[[]T]{Data: data}
			} else {
				results[i] = &dataloader.Result[[]T]{Data: []T{}}
			}
		}
		return results
	}

	return &TypedLoader[T]{
		Loader: dataloader.NewBatchedLoader[string, []T](
			batchFn,
			//nolint:mnd // default is 0, need to set a value
			dataloader.WithBatchCapacity[string, []T](100),
		),
	}
}

// Load returns the loaded data for the given key.
func (tl *TypedLoader[T]) Load(ctx context.Context, key string) ([]T, error) {
	return tl.Loader.Load(ctx, key)()
}
