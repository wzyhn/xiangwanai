package wechatpay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	wechatpaycore "github.com/wechatpay-apiv3/wechatpay-go/core"
	wechatpayments "github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/jsapi"
)

func TestProviderMapsOnlyServerOwnedPrepayFacts(t *testing.T) {
	expiresAt := time.Date(2026, 9, 13, 9, 10, 0, 0, time.UTC)
	fake := &fakePrepayClient{response: validSDKPrepayResponse()}
	fake.apiResult = &wechatpaycore.APIResult{Response: &http.Response{
		Header: http.Header{"Request-Id": []string{"provider-request-1"}},
	}}
	provider := &Provider{merchantID: "1900000109", client: fake}
	request := validProviderRequest(expiresAt)
	result, err := provider.CreateMiniProgramPrepay(context.Background(), request)
	if err != nil {
		t.Fatalf("create prepay: %v", err)
	}
	if fake.request.Appid == nil || *fake.request.Appid != request.AppID ||
		fake.request.Mchid == nil || *fake.request.Mchid != request.MerchantID ||
		fake.request.OutTradeNo == nil || *fake.request.OutTradeNo != request.OutTradeNo ||
		fake.request.TimeExpire == nil || !fake.request.TimeExpire.Equal(expiresAt) ||
		fake.request.Amount == nil || fake.request.Amount.Total == nil ||
		*fake.request.Amount.Total != request.AmountCents ||
		fake.request.Payer == nil || fake.request.Payer.Openid == nil ||
		*fake.request.Payer.Openid != request.OpenID {
		t.Fatalf("unexpected SDK request: %+v", fake.request)
	}
	if result.ProviderRequestID != "provider-request-1" ||
		result.Parameters.Package != "prepay_id="+result.PrepayID {
		t.Fatalf("unexpected provider result: %+v", result)
	}
}

func TestProviderClassifiesFailuresWithoutReturningRawDetails(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantClass string
		wantCode  string
	}{
		{name: "timeout", err: context.DeadlineExceeded, wantClass: payment.ProviderFailureTimeout},
		{
			name: "provider duplicate",
			err: &wechatpaycore.APIError{
				StatusCode: http.StatusForbidden,
				Code:       "OUT_TRADE_NO_USED",
				Body:       "sensitive provider body",
			},
			wantClass: payment.ProviderFailureAmbiguous,
			wantCode:  "OUT_TRADE_NO_USED",
		},
		{
			name: "configuration rejection",
			err: &wechatpaycore.APIError{
				StatusCode: http.StatusBadRequest,
				Code:       "APPID_MCHID_NOT_MATCH",
			},
			wantClass: payment.ProviderFailureRejected,
			wantCode:  "APPID_MCHID_NOT_MATCH",
		},
		{name: "transport", err: errors.New("socket failed"), wantClass: payment.ProviderFailureAmbiguous},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &Provider{
				merchantID: "1900000109",
				client:     &fakePrepayClient{err: test.err},
			}
			_, err := provider.CreateMiniProgramPrepay(
				context.Background(),
				validProviderRequest(time.Now().Add(time.Minute)),
			)
			class, code := payment.ProviderFailureMetadata(err)
			if class != test.wantClass || code != test.wantCode {
				t.Fatalf("got class=%q code=%q error=%v", class, code, err)
			}
		})
	}
}

func TestProviderRejectsMalformedVerifiedResponse(t *testing.T) {
	malformed := validSDKPrepayResponse()
	malformed.Appid = wechatpaycore.String("wxOtherApp")
	provider := &Provider{
		merchantID: "1900000109",
		client:     &fakePrepayClient{response: malformed},
	}
	_, err := provider.CreateMiniProgramPrepay(
		context.Background(),
		validProviderRequest(time.Now().Add(time.Minute)),
	)
	class, _ := payment.ProviderFailureMetadata(err)
	if class != payment.ProviderFailureInvalidResponse {
		t.Fatalf("expected invalid response, got %v", err)
	}
}

func TestProviderQueriesMerchantOrderAndProjectsVerifiedPaymentFacts(t *testing.T) {
	t.Parallel()

	request := validProviderPaymentQueryRequest()
	client := &fakePaymentQueryClient{
		response: validSDKPaymentQueryResponse(),
		apiResult: &wechatpaycore.APIResult{Response: &http.Response{
			Header: http.Header{"Request-Id": []string{"query-request-1"}},
		}},
	}
	provider := &Provider{merchantID: request.MerchantID, query: client}
	result, err := provider.QueryPaymentByOutTradeNo(context.Background(), request)
	if err != nil {
		t.Fatalf("QueryPaymentByOutTradeNo() error = %v", err)
	}
	if client.request.Mchid == nil || *client.request.Mchid != request.MerchantID ||
		client.request.OutTradeNo == nil ||
		*client.request.OutTradeNo != request.OutTradeNo ||
		result.AppID != request.AppID || result.MerchantID != request.MerchantID ||
		result.OutTradeNo != request.OutTradeNo || result.AmountCents != request.AmountCents ||
		result.Currency != payment.PaymentQueryCurrency ||
		result.TradeState != payment.ProviderTradeStateSuccess ||
		result.TransactionID != "wechat-transaction-1" ||
		result.SuccessAt == nil || result.ProviderRequestID != "query-request-1" {
		t.Fatalf("query request=%+v result=%+v", client.request, result)
	}
}

func TestProviderRejectsMismatchedPaymentQueryResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*wechatpayments.Transaction)
	}{
		{name: "merchant", mutate: func(value *wechatpayments.Transaction) {
			value.Mchid = wechatpaycore.String("1900000110")
		}},
		{name: "amount", mutate: func(value *wechatpayments.Transaction) {
			value.Amount.Total = wechatpaycore.Int64(1)
		}},
		{name: "state", mutate: func(value *wechatpayments.Transaction) {
			value.TradeState = wechatpaycore.String("UNRECOGNIZED")
		}},
		{name: "success time", mutate: func(value *wechatpayments.Transaction) {
			value.SuccessTime = wechatpaycore.String("not-a-time")
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := validSDKPaymentQueryResponse()
			test.mutate(response)
			request := validProviderPaymentQueryRequest()
			provider := &Provider{
				merchantID: request.MerchantID,
				query:      &fakePaymentQueryClient{response: response},
			}
			_, err := provider.QueryPaymentByOutTradeNo(context.Background(), request)
			class, _ := payment.ProviderFailureMetadata(err)
			if class != payment.ProviderFailureInvalidResponse {
				t.Fatalf("query error = %v class=%q", err, class)
			}
		})
	}
}

func TestProviderAcceptsSparseUnpaidQueryResponse(t *testing.T) {
	for _, state := range []payment.ProviderTradeState{
		payment.ProviderTradeStateNotPay, payment.ProviderTradeStateClosed,
	} {
		for _, withAmount := range []bool{true, false} {
			t.Run(string(state)+fmt.Sprint(withAmount), func(t *testing.T) {
				request := validProviderPaymentQueryRequest()
				response := validSDKPaymentQueryResponse()
				response.TradeState = wechatpaycore.String(string(state))
				response.TradeType = nil
				response.TransactionId = nil
				response.SuccessTime = nil
				if !withAmount {
					response.Amount = nil
				}
				provider := &Provider{merchantID: request.MerchantID, query: &fakePaymentQueryClient{response: response}}
				result, err := provider.QueryPaymentByOutTradeNo(context.Background(), request)
				if err != nil || result.TradeState != state || result.TradeType != payment.PaymentQueryTradeType || result.AmountCents != request.AmountCents || result.Currency != request.Currency || result.TransactionID != "" || result.SuccessAt != nil {
					t.Fatalf("sparse unpaid query result=%+v error=%v", result, err)
				}
				if response.TradeType != nil || (!withAmount && response.Amount != nil) {
					t.Fatal("provider response mutated")
				}
			})
		}
	}
}

func TestProviderSparseUnpaidQueryStillRejectsConflictingFacts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*wechatpayments.Transaction)
	}{
		{"merchant", func(r *wechatpayments.Transaction) { r.Mchid = wechatpaycore.String("1900000200") }},
		{"app", func(r *wechatpayments.Transaction) { r.Appid = wechatpaycore.String("wxOtherApp123") }},
		{"order", func(r *wechatpayments.Transaction) { r.OutTradeNo = wechatpaycore.String("OTHER-ORDER-123") }},
		{"type", func(r *wechatpayments.Transaction) { r.TradeType = wechatpaycore.String("NATIVE") }},
		{"empty type", func(r *wechatpayments.Transaction) { r.TradeType = wechatpaycore.String("") }},
		{"amount", func(r *wechatpayments.Transaction) { r.Amount.Total = wechatpaycore.Int64(1) }},
		{"currency", func(r *wechatpayments.Transaction) { r.Amount.Currency = wechatpaycore.String("USD") }},
		{"paid missing type", func(r *wechatpayments.Transaction) { r.TradeState = wechatpaycore.String("SUCCESS") }},
		{"refund missing type", func(r *wechatpayments.Transaction) { r.TradeState = wechatpaycore.String("REFUND") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := validProviderPaymentQueryRequest()
			response := validSDKPaymentQueryResponse()
			response.TradeState = wechatpaycore.String("NOTPAY")
			response.TradeType = nil
			response.TransactionId = nil
			response.SuccessTime = nil
			test.mutate(response)
			provider := &Provider{merchantID: request.MerchantID, query: &fakePaymentQueryClient{response: response}}
			_, err := provider.QueryPaymentByOutTradeNo(context.Background(), request)
			class, _ := payment.ProviderFailureMetadata(err)
			if class != payment.ProviderFailureInvalidResponse {
				t.Fatalf("error=%v class=%q", err, class)
			}
		})
	}
}

func TestProviderClosesOnlyServerOwnedMerchantOrder(t *testing.T) {
	t.Parallel()

	request := payment.ProviderPaymentCloseRequest{
		AppID:      "wxXiangwan123",
		MerchantID: "1900000109",
		OutTradeNo: "XW-PREPAY-ORDER-00000001",
	}
	client := &fakePaymentCloseClient{apiResult: &wechatpaycore.APIResult{
		Response: &http.Response{Header: http.Header{
			"Request-Id": []string{"close-request-1"},
		}},
	}}
	provider := &Provider{
		merchantID: request.MerchantID,
		close:      client,
	}
	result, err := provider.ClosePaymentByOutTradeNo(
		context.Background(),
		request,
	)
	if err != nil || client.request.Mchid == nil ||
		*client.request.Mchid != request.MerchantID ||
		client.request.OutTradeNo == nil ||
		*client.request.OutTradeNo != request.OutTradeNo ||
		result.ProviderRequestID != "close-request-1" {
		t.Fatalf("ClosePaymentByOutTradeNo() request=%+v result=%+v err=%v", client.request, result, err)
	}
}

func TestProviderClassifiesCloseFailure(t *testing.T) {
	t.Parallel()

	request := payment.ProviderPaymentCloseRequest{
		AppID:      "wxXiangwan123",
		MerchantID: "1900000109",
		OutTradeNo: "XW-PREPAY-ORDER-00000001",
	}
	provider := &Provider{
		merchantID: request.MerchantID,
		close: &fakePaymentCloseClient{err: &wechatpaycore.APIError{
			StatusCode: http.StatusConflict,
			Code:       "ORDER_CLOSED",
		}},
	}
	_, err := provider.ClosePaymentByOutTradeNo(context.Background(), request)
	class, code := payment.ProviderFailureMetadata(err)
	if class != payment.ProviderFailureRejected || code != "ORDER_CLOSED" {
		t.Fatalf("close failure class=%q code=%q err=%v", class, code, err)
	}
}

type fakePrepayClient struct {
	request   jsapi.PrepayRequest
	response  *jsapi.PrepayWithRequestPaymentResponse
	apiResult *wechatpaycore.APIResult
	err       error
}

type fakePaymentQueryClient struct {
	request   jsapi.QueryOrderByOutTradeNoRequest
	response  *wechatpayments.Transaction
	apiResult *wechatpaycore.APIResult
	err       error
}

type fakePaymentCloseClient struct {
	request   jsapi.CloseOrderRequest
	apiResult *wechatpaycore.APIResult
	err       error
}

func (client *fakePaymentQueryClient) QueryOrderByOutTradeNo(
	_ context.Context,
	request jsapi.QueryOrderByOutTradeNoRequest,
) (*wechatpayments.Transaction, *wechatpaycore.APIResult, error) {
	client.request = request
	return client.response, client.apiResult, client.err
}

func (client *fakePaymentCloseClient) CloseOrder(
	_ context.Context,
	request jsapi.CloseOrderRequest,
) (*wechatpaycore.APIResult, error) {
	client.request = request
	return client.apiResult, client.err
}

func (client *fakePrepayClient) PrepayWithRequestPayment(
	_ context.Context,
	request jsapi.PrepayRequest,
) (*jsapi.PrepayWithRequestPaymentResponse, *wechatpaycore.APIResult, error) {
	client.request = request
	return client.response, client.apiResult, client.err
}

func validProviderRequest(expiresAt time.Time) payment.ProviderPrepayRequest {
	return payment.ProviderPrepayRequest{
		AppID:        "wxXiangwan123",
		MerchantID:   "1900000109",
		Description:  "享玩活动报名",
		OutTradeNo:   "XW-PREPAY-ORDER-00000001",
		NotifyURL:    "https://xiangwan.example.com/api/v1/xiangwan/wechat-pay/notifications",
		OpenID:       "openid-xiangwan-1",
		AmountCents:  19900,
		TimeExpireAt: expiresAt,
	}
}

func validSDKPrepayResponse() *jsapi.PrepayWithRequestPaymentResponse {
	prepayID := "wx201410272009395522657a690389285100"
	return &jsapi.PrepayWithRequestPaymentResponse{
		PrepayId:  wechatpaycore.String(prepayID),
		Appid:     wechatpaycore.String("wxXiangwan123"),
		TimeStamp: wechatpaycore.String("1789290000"),
		NonceStr:  wechatpaycore.String("nonce-1"),
		Package:   wechatpaycore.String("prepay_id=" + prepayID),
		SignType:  wechatpaycore.String("RSA"),
		PaySign:   wechatpaycore.String("signed-payment-parameters"),
	}
}

func validProviderPaymentQueryRequest() payment.ProviderPaymentQueryRequest {
	return payment.ProviderPaymentQueryRequest{
		AppID:       "wxXiangwan123",
		MerchantID:  "1900000109",
		OutTradeNo:  "XW-PREPAY-ORDER-00000001",
		AmountCents: 19900,
		Currency:    payment.PaymentQueryCurrency,
	}
}

func validSDKPaymentQueryResponse() *wechatpayments.Transaction {
	return &wechatpayments.Transaction{
		Appid:         wechatpaycore.String("wxXiangwan123"),
		Mchid:         wechatpaycore.String("1900000109"),
		OutTradeNo:    wechatpaycore.String("XW-PREPAY-ORDER-00000001"),
		TransactionId: wechatpaycore.String("wechat-transaction-1"),
		TradeType:     wechatpaycore.String(payment.PaymentQueryTradeType),
		TradeState:    wechatpaycore.String(string(payment.ProviderTradeStateSuccess)),
		SuccessTime:   wechatpaycore.String("2026-09-19T02:03:04+00:00"),
		Amount: &wechatpayments.TransactionAmount{
			Total:    wechatpaycore.Int64(19900),
			Currency: wechatpaycore.String(payment.PaymentQueryCurrency),
		},
	}
}
