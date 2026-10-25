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
// The batch results are returned in key order; a key absent from the fetched map yields an
// empty slice and a fetch failure fails every key of the batch.
func NewTypedLoader[T any](batchFetchFunc BatchFetchFuncTyped[T], errorMessage string) *TypedLoader[T] {
	batchFn := func(ctx context.Context, keys []string) []*dataloader.Result[[]T] {
		dataMap, err := batchFetchFunc(ctx, keys)
		results := make([]*dataloader.Result[[]T], len(keys))
		if err != nil {
			for i := range keys {
				results[i] = &dataloader.Result[[]T]{
					Error: fmt.Errorf("%s: %w", errorMessage, err),
				}
			}
			return results
		}

		for i, key := range keys {
			if data, ok := dataMap[key]; ok {
				results[i] = &dataloader.Result[[]T]{Data: data}
			} else {
				results[i] = &dataloader.Result[[]T]{Data: []T{}}
			}
		}
		return results
	}

	return &TypedLoader[T]{
		Loader: dataloader.NewBatchedLoader(
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
