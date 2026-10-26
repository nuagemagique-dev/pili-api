package resolver

import (
	"context"
	"errors"

	"go.uber.org/zap"

	notificationApplication "tsb-service/internal/modules/notification/application"
	orderDomain "tsb-service/internal/modules/order/domain"
	"tsb-service/pkg/apns"
	"tsb-service/pkg/fcm"
)

// SendNewOrderPush fans out a "new order" push notification to admin devices
// (phones / dashboard) and POS handhelds. Safe to call when FCM and APNs are
// both unconfigured (no-op). Runs in its own goroutine: callers should not
// wrap it in `go`.
//
// Online-payment orders must only call this once the Mollie webhook confirms
// payment; cash orders call it at creation time.
func (r *Resolver) SendNewOrderPush(order *orderDomain.Order) {
	if r.FCMClient == nil && r.APNsClient == nil {
		return
	}
	if order == nil {
		return
	}
	// Store-review test orders never alert staff (no admin push, no POS push).
	// TEMPORARY (revert after launch).
	if order.IsTest {
		zap.L().Info("suppressing new-order push for store-review test order",
			zap.String("order_id", order.ID.String()),
		)
		return
	}

	go func() {
		msg := notificationApplication.GetNewOrderNotification(order.Language, string(order.OrderType), order.TotalPrice.StringFixed(2))
		data := map[string]string{
			"orderId": order.ID.String(),
			"type":    "new_order",
		}

		// Admin devices (phones / dashboard). Independent of POS devices: an
		// empty admin list must NOT short-circuit POS delivery.
		adminTokens, tokenErr := r.NotificationService.GetAdminDeviceTokens(context.Background())
		if tokenErr != nil {
			zap.L().Warn("failed to fetch admin device tokens",
				zap.String("order_id", order.ID.String()),
				zap.Error(tokenErr),
			)
		}
		for _, dt := range adminTokens {
			if dt.Platform == "android" && r.FCMClient != nil {
				if pushErr := r.FCMClient.SendAlert(dt.DeviceToken, msg.Title, msg.Body, data); pushErr != nil {
					if errors.Is(pushErr, fcm.ErrTokenInvalid) {
						_ = r.NotificationService.UnregisterDeviceToken(context.Background(), dt.UserID, dt.DeviceToken)
					} else {
						zap.L().Error("failed to send admin FCM push",
							zap.String("order_id", order.ID.String()),
							zap.Error(pushErr),
						)
					}
				}
			} else if dt.Platform == "ios" && r.APNsClient != nil {
				if pushErr := r.APNsClient.SendAlert(dt.DeviceToken, msg.Title, msg.Body, data); pushErr != nil {
					if errors.Is(pushErr, apns.ErrTokenInvalid) {
						_ = r.NotificationService.UnregisterDeviceToken(context.Background(), dt.UserID, dt.DeviceToken)
					} else {
						zap.L().Error("failed to send admin APNs push",
							zap.String("order_id", order.ID.String()),
							zap.Error(pushErr),
						)
					}
				}
			}
		}

		// POS devices (Sunmi handhelds): an FCM token, or, on FCM direct send, an installation ID.
		if r.PosService != nil && r.FCMClient != nil {
			posTokens, posErr := r.PosService.GetActiveFCMTokens(context.Background())
			if posErr != nil {
				zap.L().Warn("failed to fetch POS FCM tokens",
					zap.String("order_id", order.ID.String()),
					zap.Error(posErr),
				)
			}
			posFids, fidErr := r.PosService.GetActiveFCMInstallationIDs(context.Background())
			if fidErr != nil {
				zap.L().Warn("failed to fetch POS FCM installation IDs",
					zap.String("order_id", order.ID.String()),
					zap.Error(fidErr),
				)
			}
			zap.L().Info("sending POS FCM pushes",
				zap.String("order_id", order.ID.String()),
				zap.Int("token_count", len(posTokens)),
				zap.Int("installation_count", len(posFids)),
			)
			send := func(to string, viaFid bool) {
				var pushErr error
				if viaFid {
					pushErr = r.FCMClient.SendAlertToInstallation(to, msg.Title, msg.Body, data)
				} else {
					pushErr = r.FCMClient.SendAlert(to, msg.Title, msg.Body, data)
				}
				if pushErr != nil {
					zap.L().Warn("failed to send POS FCM push",
						zap.String("order_id", order.ID.String()),
						zap.Error(pushErr),
					)
				}
			}
			for _, token := range posTokens {
				send(token, false)
			}
			for _, fid := range posFids {
				send(fid, true)
			}
		}
	}()
}
