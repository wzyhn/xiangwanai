// Package wechatpay provides Xiangwan's private WeChat Pay API v3 adapter.
// Provider identities and amounts come only from server-owned Order facts.
package wechatpay

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	wechatpaycore "github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	wechatpayments "github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/jsapi"
)

const DefaultHTTPTimeout = 8 * time.Second

var (
	ErrInvalidProviderConfig = errors.New("invalid xiangwan WeChat Pay provider config")
	merchantIDPattern        = regexp.MustCompile(`^[0-9]{6,32}$`)
	certificateSerialPattern = regexp.MustCompile(`^[0-9A-F]{16,64}$`)
	publicKeyIDPattern       = regexp.MustCompile(`^PUB_KEY_ID_[0-9A-Za-z_]{1,64}$`)
)

type Config struct {
	MerchantID                string
	MerchantCertificateSerial string
	MerchantPrivateKey        *rsa.PrivateKey
	WeChatPayPublicKeyID      string
	WeChatPayPublicKey        *rsa.PublicKey
	HTTPClient                *http.Client
}

type prepayClient interface {
	PrepayWithRequestPayment(
		context.Context,
		jsapi.PrepayRequest,
	) (*jsapi.PrepayWithRequestPaymentResponse, *wechatpaycore.APIResult, error)
}

type paymentQueryClient interface {
	QueryOrderByOutTradeNo(
		context.Context,
		jsapi.QueryOrderByOutTradeNoRequest,
	) (*wechatpayments.Transaction, *wechatpaycore.APIResult, error)
}

type paymentCloseClient interface {
	CloseOrder(
		context.Context,
		jsapi.CloseOrderRequest,
	) (*wechatpaycore.APIResult, error)
}

type Provider struct {
	merchantID string
	client     prepayClient
	query      paymentQueryClient
	close      paymentCloseClient
}

var _ payment.PaymentCloseProviderPort = (*Provider)(nil)

func NewProvider(ctx context.Context, config Config) (*Provider, error) {
	if ctx == nil || !validConfig(config) {
		return nil, ErrInvalidProviderConfig
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	client, err := wechatpaycore.NewClient(
		ctx,
		option.WithWechatPayPublicKeyAuthCipher(
			config.MerchantID,
			config.MerchantCertificateSerial,
			config.MerchantPrivateKey,
			config.WeChatPayPublicKeyID,
			config.WeChatPayPublicKey,
		),
		option.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, errors.Join(ErrInvalidProviderConfig, err)
	}
	service := &jsapi.JsapiApiService{Client: client}
	return &Provider{
		merchantID: config.MerchantID,
		client:     service,
		query:      service,
		close:      service,
	}, nil
}

func (provider *Provider) CreateMiniProgramPrepay(
	ctx context.Context,
	request payment.ProviderPrepayRequest,
) (payment.ProviderPrepayResult, error) {
	if provider == nil || provider.client == nil || ctx == nil ||
		request.MerchantID != provider.merchantID ||
		payment.ValidateProviderPrepayRequest(request) != nil {
		return payment.ProviderPrepayResult{}, payment.ErrInvalidProviderPrepay
	}
	response, apiResult, err := provider.client.PrepayWithRequestPayment(
		ctx,
		jsapi.PrepayRequest{
			Appid:       wechatpaycore.String(request.AppID),
			Mchid:       wechatpaycore.String(request.MerchantID),
			Description: wechatpaycore.String(request.Description),
			OutTradeNo:  wechatpaycore.String(request.OutTradeNo),
			TimeExpire:  wechatpaycore.Time(request.TimeExpireAt.UTC()),
			NotifyUrl:   wechatpaycore.String(request.NotifyURL),
			Amount: &jsapi.Amount{
				Total:    wechatpaycore.Int64(request.AmountCents),
				Currency: wechatpaycore.String(payment.PrepayCurrency),
			},
			Payer: &jsapi.Payer{Openid: wechatpaycore.String(request.OpenID)},
		},
	)
	if err != nil {
		class, code := classifyProviderError(err)
		return payment.ProviderPrepayResult{}, payment.NewProviderFailure(class, code, err)
	}
	result, err := projectProviderResult(response, apiResult)
	if err != nil {
		return payment.ProviderPrepayResult{}, payment.NewProviderFailure(
			payment.ProviderFailureInvalidResponse,
			"",
			err,
		)
	}
	if err := payment.ValidateProviderPrepayResult(result, request.AppID); err != nil {
		return payment.ProviderPrepayResult{}, payment.NewProviderFailure(
			payment.ProviderFailureInvalidResponse,
			"",
			err,
		)
	}
	return result, nil
}

func (provider *Provider) QueryPaymentByOutTradeNo(
	ctx context.Context,
	request payment.ProviderPaymentQueryRequest,
) (payment.ProviderPaymentQueryResult, error) {
	if provider == nil || provider.query == nil || ctx == nil ||
		request.MerchantID != provider.merchantID ||
		payment.ValidateProviderPaymentQueryRequest(request) != nil {
		return payment.ProviderPaymentQueryResult{}, payment.ErrInvalidPaymentQuery
	}
	providerResponse, apiResult, err := provider.query.QueryOrderByOutTradeNo(
		ctx,
		jsapi.QueryOrderByOutTradeNoRequest{
			OutTradeNo: wechatpaycore.String(request.OutTradeNo),
			Mchid:      wechatpaycore.String(request.MerchantID),
		},
	)
	if err != nil {
		class, code := classifyProviderError(err)
		return payment.ProviderPaymentQueryResult{}, payment.NewProviderFailure(
			class,
			code,
			err,
		)
	}
	result, err := projectMiniProgramQueryResult(providerResponse, apiResult, request)
	if err != nil || payment.ValidateProviderPaymentQueryResult(result, request) != nil {
		if err == nil {
			err = payment.ErrInvalidPaymentQuery
		}
		return payment.ProviderPaymentQueryResult{}, payment.NewProviderFailure(
			payment.ProviderFailureInvalidResponse,
			"",
			err,
		)
	}
	return result, nil
}

func (provider *Provider) ClosePaymentByOutTradeNo(
	ctx context.Context,
	request payment.ProviderPaymentCloseRequest,
) (payment.ProviderPaymentCloseResult, error) {
	if provider == nil || provider.close == nil || ctx == nil ||
		request.MerchantID != provider.merchantID ||
		payment.ValidateProviderPaymentCloseRequest(request) != nil {
		return payment.ProviderPaymentCloseResult{},
			payment.ErrInvalidPaymentCloseJob
	}
	apiResult, err := provider.close.CloseOrder(
		ctx,
		jsapi.CloseOrderRequest{
			OutTradeNo: wechatpaycore.String(request.OutTradeNo),
			Mchid:      wechatpaycore.String(request.MerchantID),
		},
	)
	if err != nil {
		class, code := classifyProviderError(err)
		return payment.ProviderPaymentCloseResult{}, payment.NewProviderFailure(
			class,
			code,
			err,
		)
	}
	result := payment.ProviderPaymentCloseResult{
		ProviderRequestID: providerRequestID(apiResult),
	}
	if err := payment.ValidateProviderPaymentCloseResult(result); err != nil {
		return payment.ProviderPaymentCloseResult{}, payment.NewProviderFailure(
			payment.ProviderFailureInvalidResponse,
			"",
			err,
		)
	}
	return result, nil
}

func validConfig(config Config) bool {
	if !merchantIDPattern.MatchString(config.MerchantID) ||
		!certificateSerialPattern.MatchString(config.MerchantCertificateSerial) ||
		config.MerchantPrivateKey == nil ||
		!publicKeyIDPattern.MatchString(config.WeChatPayPublicKeyID) ||
		config.WeChatPayPublicKey == nil {
		return false
	}
	return config.HTTPClient == nil ||
		(config.HTTPClient.Timeout > 0 && config.HTTPClient.Timeout <= 30*time.Second)
}

func projectProviderResult(
	response *jsapi.PrepayWithRequestPaymentResponse,
	apiResult *wechatpaycore.APIResult,
) (payment.ProviderPrepayResult, error) {
	if response == nil || response.PrepayId == nil || response.Appid == nil ||
		response.TimeStamp == nil || response.NonceStr == nil ||
		response.Package == nil || response.SignType == nil ||
		response.PaySign == nil {
		return payment.ProviderPrepayResult{}, payment.ErrInvalidProviderPrepay
	}
	return payment.ProviderPrepayResult{
		PrepayID:          *response.PrepayId,
		ProviderRequestID: providerRequestID(apiResult),
		Parameters: payment.MiniProgramPaymentParameters{
			AppID:     *response.Appid,
			TimeStamp: *response.TimeStamp,
			NonceStr:  *response.NonceStr,
			Package:   *response.Package,
			SignType:  *response.SignType,
			PaySign:   *response.PaySign,
		},
	}, nil
}

// Unpaid query responses may omit trade_type and amount metadata. They identify
// the order by AppID, merchant and out_trade_no; the missing display metadata
// comes from the server-owned mini-program Order, never from a client or a paid
// inference. Present metadata still has to match, and paid/notification results
// retain the strict projection below.
func projectMiniProgramQueryResult(
	response *wechatpayments.Transaction,
	apiResult *wechatpaycore.APIResult,
	request payment.ProviderPaymentQueryRequest,
) (payment.ProviderPaymentQueryResult, error) {
	if response == nil || response.TradeState == nil ||
		(*response.TradeState != string(payment.ProviderTradeStateNotPay) &&
			*response.TradeState != string(payment.ProviderTradeStateClosed)) {
		return projectPaymentQueryResult(response, apiResult)
	}
	normalized := *response
	if normalized.TradeType == nil {
		normalized.TradeType = wechatpaycore.String(payment.PaymentQueryTradeType)
	}
	amount := wechatpayments.TransactionAmount{}
	if response.Amount != nil {
		amount = *response.Amount
	}
	if amount.Total == nil {
		amount.Total = wechatpaycore.Int64(request.AmountCents)
	}
	if amount.Currency == nil {
		amount.Currency = wechatpaycore.String(request.Currency)
	}
	normalized.Amount = &amount
	return projectPaymentQueryResult(&normalized, apiResult)
}

func projectPaymentQueryResult(
	response *wechatpayments.Transaction,
	apiResult *wechatpaycore.APIResult,
) (payment.ProviderPaymentQueryResult, error) {
	if response == nil || response.Appid == nil || response.Mchid == nil ||
		response.OutTradeNo == nil || response.TradeType == nil ||
		response.TradeState == nil || response.Amount == nil ||
		response.Amount.Total == nil || response.Amount.Currency == nil {
		return payment.ProviderPaymentQueryResult{}, payment.ErrInvalidPaymentQuery
	}
	result := payment.ProviderPaymentQueryResult{
		AppID:             *response.Appid,
		MerchantID:        *response.Mchid,
		OutTradeNo:        *response.OutTradeNo,
		TradeType:         *response.TradeType,
		TradeState:        payment.ProviderTradeState(*response.TradeState),
		AmountCents:       *response.Amount.Total,
		Currency:          *response.Amount.Currency,
		ProviderRequestID: providerRequestID(apiResult),
	}
	if response.TransactionId != nil {
		result.TransactionID = *response.TransactionId
	}
	if response.SuccessTime != nil {
		successAt, err := time.Parse(time.RFC3339, *response.SuccessTime)
		if err != nil {
			return payment.ProviderPaymentQueryResult{}, payment.ErrInvalidPaymentQuery
		}
		successAt = successAt.UTC()
		result.SuccessAt = &successAt
	}
	return result, nil
}

func providerRequestID(apiResult *wechatpaycore.APIResult) string {
	if apiResult == nil || apiResult.Response == nil {
		return ""
	}
	requestID := strings.TrimSpace(apiResult.Response.Header.Get("Request-ID"))
	if len([]rune(requestID)) > 128 || strings.ContainsAny(requestID, "\r\n\x00") {
		return ""
	}
	return requestID
}

func classifyProviderError(err error) (string, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return payment.ProviderFailureTimeout, ""
	}
	var apiError *wechatpaycore.APIError
	if !errors.As(err, &apiError) || apiError == nil {
		return payment.ProviderFailureAmbiguous, ""
	}
	code := strings.TrimSpace(apiError.Code)
	if apiError.StatusCode == http.StatusTooManyRequests ||
		apiError.StatusCode >= http.StatusInternalServerError ||
		code == "OUT_TRADE_NO_USED" {
		return payment.ProviderFailureAmbiguous, code
	}
	return payment.ProviderFailureRejected, code
}
