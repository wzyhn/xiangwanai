package xiangwanapi

import (
	"errors"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
)

func TestCustomerErrorLocalizesPublicBusinessErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		message string
	}{
		{name: "bad request", err: errx.NewBadRequest("invalid Session time"), message: "提交内容有误，请检查后重试"},
		{name: "capacity conflict", err: errx.NewConflict("Session capacity is full"), message: "名额刚刚发生变化，请刷新后重试"},
		{name: "admin publication", err: errx.New(errx.CodeXiangwanAdminPublicationInvalid, "publication validation failed"), message: "发布检查未通过，请按提示完善内容"},
		{name: "wrapped", err: errors.Join(errors.New("service"), errx.NewNotFound("Registration not found")), message: "相关内容不存在或已下线"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var got *errx.Error
			if !errors.As(customerError(test.err), &got) {
				t.Fatal("customerError did not preserve the typed error")
			}
			if got.Message != test.message {
				t.Fatalf("message = %q, want %q", got.Message, test.message)
			}
		})
	}
}

func TestCustomerErrorPreservesInternalErrorForLogging(t *testing.T) {
	t.Parallel()
	original := errx.NewInternal("PostgreSQL is unavailable")
	if got := customerError(original); got != original {
		t.Fatal("internal error must retain its original chain for the response logger")
	}
}
