package resolver

import (
	"context"

	"go.uber.org/zap"

	"tsb-service/internal/modules/product/domain"
	"tsb-service/pkg/logging"
)

// markImageUpdated records a successful image upload on prod, so clients get a new image URL.
// A failure is logged: the image is stored, only its cache-busting version stays behind.
func (r *mutationResolver) markImageUpdated(ctx context.Context, prod *domain.Product) {
	updatedAt, err := r.ProductService.MarkProductImageUpdated(ctx, prod.ID)
	if err != nil {
		logging.FromContext(ctx).Error("failed to record image update", zap.String("product_id", prod.ID.String()), zap.Error(err))
		return
	}
	prod.ImageUpdatedAt = &updatedAt
}
