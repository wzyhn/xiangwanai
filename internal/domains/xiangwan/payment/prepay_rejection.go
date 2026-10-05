package payment

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrPrepayRejected = errors.New("xiangwan prepay was rejected without creating a payment")
var ErrPrepayRejectionUnproven = errors.New("xiangwan prepay rejection cannot safely close this order")

type RejectedPrepayClosure struct {
	TenantID    uuid.UUID
	OrderID     uuid.UUID
	PrincipalID uuid.UUID
}

// The adapter must recheck durable first-invocation evidence under the Order
// lock. A rejected retry does not disprove an earlier successful/unknown call.
type RejectedPrepayCloser interface {
	CloseRejectedPrepay(context.Context, RejectedPrepayClosure) (PaymentConvergence, error)
}

func IsDefinitivePrepayRejectionCode(code string) bool {
	switch code {
	case "NO_AUTH", "APPID_MCHID_NOT_MATCH", "MCH_NOT_EXISTS", "PARAM_ERROR", "INVALID_REQUEST", "SIGN_ERROR":
		return true
	default:
		return false
	}
}
