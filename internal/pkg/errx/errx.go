// Package errx defines the platform-wide business error type and the
// numeric code namespace shared by all modules.
//
// Code ranges:
//
//   - 0:               OK (success;响应层标记)
//   - 10001-10999:     generic (BadRequest / Unauthorized / Forbidden / etc.)
//   - 20xxx:           L1 / L2 platform capability segments
//     20001-20099:     auth (identity)
//     20101-20199:     tenancy (space — personal scope resolver; ADR-034)
//     20301-20399:     storage (file)
//   - 30xxx:           L3 product domain segments
//     30001-30099:     community
//     30101-30199:     study (note / parse — 不含 schedule/timetable)
//     30201-30299:     record (audit / cascade / transcode 全部归此段)
//     30401-30499:     schedule / timetable (D-03 中期 C 拆 wq-schedule
//     独立模块时此段保持稳定;client 不被 study renumber)
//     30501-30599:     laoa (老A enrollment / operation ledger; ADR-034 消费者)
//     30601-30699:     xiangwan administrator Web
//   - 40xxx:           L2 cross-cutting segments
//     40001-40099:     ai
//     40101-40199:     billing
//     40201-40299:     syncpkg (share snapshot / receiver save)
//
// schedule 段 30401-30499:schedule HANDOVER §7.12 D-03 中期 C 不可省硬约束 —
// 短期 A 复用 study/schedules + product_code=wq-schedule;中期 C 切独立
// /api/v1/timetable/*。错误码段独立避免 client 在 split 时被 breaking
// renumber 砸到。
//
// record 段 30201-30299:ADR-017 record OUT closed beta scope;codes 占位
// 仅 namespace 保留,真业务路径 Phase 3.5 V1 工程(V1-T7/T12/T14a)时
// wire up。本 PR 不在 record 模块实际触发这些 codes。
//
// 注:audit_status=pending 等 "状态非终态" 场景不进 errx — 它们是 success
// payload 的 state 字段,response.OK + Body.Data.audit_status="pending"。
// errx 段只表达"错误事实"(已发生 + 客户端需 abort)。
package errx

import "fmt"

// Code represents a business error code. Numeric value is the wire format;
// see codeToHTTP in internal/pkg/response for HTTP status mapping.
type Code int

// Generic error codes (10001-10006).
const (
	CodeOK           Code = 0
	CodeBadRequest   Code = 10001
	CodeUnauthorized Code = 10002
	CodeForbidden    Code = 10003
	CodeNotFound     Code = 10004
	CodeConflict     Code = 10005
	CodeInternal     Code = 10006
)

// Xiangwan administrator segment (30601-30699).
//
// These codes belong to the independent customer operator surface. They are
// deliberately separate from consumer WeChat authentication so a browser
// session can never be interpreted as a mini-program credential (or vice
// versa).
const (
	CodeXiangwanAdminUnavailable        Code = 30601
	CodeXiangwanAdminSessionInvalid     Code = 30602
	CodeXiangwanAdminScopeForbidden     Code = 30603
	CodeXiangwanAdminOperationConflict  Code = 30604
	CodeXiangwanAdminVersionConflict    Code = 30605
	CodeXiangwanAdminPublicationInvalid Code = 30606
	CodeXiangwanAdminTargetNotFound     Code = 30607
	CodeXiangwanAdminCSRFRejected       Code = 30608
	CodeXiangwanAdminIdentityRejected   Code = 30609
	CodeXiangwanAdminLoginRateLimited   Code = 30610
	// The activity references a quick-tag code that is absent from the
	// tenant's currently published BrandProfile vocabulary (409).
	CodeXiangwanAdminQuickTagUnavailable Code = 30611
)

// Auth segment (20001-20099).
const (
	CodeWechatAuthFail Code = 20001
	// CodeInvalidStateTransition: closed-beta P0 sweep (2026-05-12 C10) —
	// admin verification approve/reject 状态机被绕过(已 approved 可被再
	// approve,已 rejected 可被再 reject)。本 code 表达"目标态非可改"业务
	// 事实,client 应弹"该用户审核状态已变更,请刷新列表"。
	CodeInvalidStateTransition Code = 20002
	// CodePrincipalInactive: ADR-034 live ActivePrincipalGate — JWT 有效但
	// claims 对应的 Principal 此刻不可用(missing / suspended / soft-deleted)。
	// 统一 typed 403,不区分三种子态以免泄漏主体存在性/生命周期。所有 LaoA
	// route、会读取/派生/呈现用户内容的 worker,以及 DevLoginByPrincipal 都
	// 以此 fail closed。无效/过期 JWT 仍由 middleware 返回 10002(401)。
	CodePrincipalInactive Code = 20003
)

// Tenancy segment (20101-20199).
//
// ADR-034 Decision B: L1 tenancy (space) 的 PersonalScopeResolver 公开合同。
// personal scope 是非组织 Principal 的私有 tenant 映射,与 primary_tenant_id
// 正交(参 D9)。code 命名为业务事实,不是命令式。
const (
	CodePersonalScopeNotFound Code = 20101 // GetForPrincipal 找不到 personal scope 映射
	CodePersonalScopeMismatch Code = 20102 // RequireForPrincipal:映射缺失或 tenant 不等于 expected
)

// Storage segment (20301-20399).
const (
	CodeFileTooLarge         Code = 20301
	CodeFileTypeNotSupported Code = 20302
)

// Community segment (30001-30099).
//
// round-2 obs P1-4: 新增段;每段 5-10 codes。命名前缀 `Code` + module +
// 业务事实(不是命令式)— 与 generic codes 保持一致风格。
const (
	CodeCommunityPostNotFound       Code = 30001 // post 主键找不到
	CodeCommunityPostNotEditable    Code = 30002 // post status 非 active(已删除/审核中等)
	CodeCommunityPostUnderReview    Code = 30003 // post 审核中,非作者不可见
	CodeCommunityCommentNotFound    Code = 30004 // comment 主键找不到
	CodeCommunityShareTargetExpired Code = 30005 // applink share token 过期/不存在
	CodeCommunityContactRateLimit   Code = 30006 // 联系频率限制(content.contact.intended)
	CodeCommunityModerationRejected Code = 30007 // 内容被 moderation 驳回
)

// Study segment (30101-30199).
//
// 注:不含 schedule / timetable 错误码 — 那部分独立到 30401-30499 schedule
// 段,避免 D-03 中期 C 拆独立 timetable 模块时 client 被 breaking renumber。
const (
	CodeStudyNoteNotFound         Code = 30101
	CodeStudyParseUnsupportedType Code = 30102 // ai.ParseDocument 拒绝的 mime type
)

// Record segment (30201-30299).
//
// 注:ADR-017 record OUT closed beta scope;codes 占位仅 namespace 保留,
// 真业务路径 Phase 3.5 V1 工程时 wire up。
//
// 不在此段:audit_status=pending(非错误态;走 response.OK + Body.Data
// state field;errx 段只表达"错误事实")
const (
	CodeRecordMomentNotFound       Code = 30201
	CodeRecordAccountCascadeFailed Code = 30202 // V1-T12 cascade hard-delete 触发失败
	CodeRecordAuditRejected        Code = 30203 // 微信审核驳回(audit_status=rejected)
	CodeRecordTranscodeFailed      Code = 30204 // 月回顾视频合成失败
)

// Schedule / timetable segment (30401-30499).
//
// 命名前缀 Schedule(不是 StudySchedule)— 反映 schedule HANDOVER §7.12
// 决议:中期 C 切独立 /api/v1/timetable/* 模块。本段独立保留,study 拆分
// 时 client 不被 renumber 砸到。
const (
	CodeScheduleNotFound          Code = 30401 // 课表主键找不到
	CodeScheduleImportFailed      Code = 30402 // AI 导入失败(parse stage failed)
	CodeScheduleTimetableConflict Code = 30403 // 课表保存冲突(三选一弹窗;PRD §13.6.5)
)

// LaoA segment (30501-30599).
//
// 老A(laoa)L3 域 — enrollment epoch 写门与 Principal-global operation 幂等账本
// (ADR-034 ActivePrincipalGate/PersonalScope 的消费者;架构 §4.3/§7.2/§8.2)。
// 命名为业务事实。inactive 主体统一走 auth 段 20003(不在本段)。
const (
	CodeLaoAEnrollmentStateChanged    Code = 30501 // activate expected_state_revision 与当前不符(409)
	CodeLaoAEnrollmentDeleting        Code = 30502 // 处于 deleting,不激活(409)
	CodeLaoAEnrollmentRetired         Code = 30503 // 旧 operation 的 epoch/revision 已退休,replay 不复活(410)
	CodeLaoAIdempotencyConflict       Code = 30504 // 同 operation_id 被另一 kind 占用(409)
	CodeLaoAFingerprintMismatch       Code = 30505 // 同 operation_id 不同 request fingerprint(409)
	CodeLaoAEnrollmentNotFound        Code = 30506 // 生命周期转换时缺 enrollment 行(404)
	CodeLaoAInvalidTransition         Code = 30507 // 非法 enrollment 状态转换(409)
	CodeLaoAEvidenceEpochRetired      Code = 30508 // evidence 命令携带非 current active epoch(410)
	CodeLaoAEvidenceNotFound          Code = 30509 // 资产不存在/非本人/不可用,同形 404
	CodeLaoAAssetConflict             Code = 30510 // 本人 asset_id 已被另一 operation 占用(409)
	CodeLaoAEvidenceVersionConflict   Code = 30511 // evidence base_version 已不是当前 head(409)
	CodeLaoAEvidenceLifecycleConflict Code = 30512 // delete/restore receipt 已被后续生命周期或 head 取代(409)
)

// AI segment (40001-40099).
const (
	CodeAIQuotaExceeded Code = 40001
	CodeAIBusy          Code = 40002
)

// Billing segment (40101-40199).
const (
	CodeBillingQuotaExceeded      Code = 40101 // 通用配额超限(与 AIQuotaExceeded 区分)
	CodeBillingEntitlementMissing Code = 40102 // 权益未授予(Lite/Pro/ai_pack)
	CodeBillingCreditInsufficient Code = 40103 // AI 信用账本不足
	CodeBillingUndoExpired        Code = 40104 // 24h 撤销已过期
	CodeBillingLedgerConflict     Code = 40105 // 账本写入冲突(并发 + idempotency)
)

// Sync package segment (40201-40299).
//
// syncpkg is the cross-cutting snapshot/share capability currently consumed by
// WQ Schedule. Keep its wire codes outside billing and the schedule CRUD range:
// the module owns package lifecycle, snapshot validity and receiver-save modes.
const (
	// Unknown, expired and revoked packages intentionally collapse to one fact
	// so anonymous callers cannot enumerate whether a bearer code once existed.
	CodeSyncPackageUnavailable Code = 40201
	// Stored/publish-time snapshot cannot produce a complete receiver timetable.
	CodeSyncPackageSnapshotInvalid Code = 40202
	// Cryptographic code generation exhausted its bounded collision retries.
	CodeSyncPackageCodeUnavailable Code = 40203
	// The requested receiver mutation mode is recognized but not implemented.
	CodeSyncPackageSaveModeUnsupported Code = 40204
	// A receiver-save operation UUID was previously committed for a different
	// normalized command and must never be reinterpreted.
	CodeSyncPackageSaveOperationConflict Code = 40205
	// The command committed, but its result is no longer the receiver's active
	// saved Content (for example after undo or a deliberate later re-save).
	CodeSyncPackageSaveOperationInactive Code = 40206
	// New public sharing writes are deliberately rollout-closed. Reads,
	// revocation and exact committed retries may remain available.
	CodeSyncPackageWritesUnavailable Code = 40207
	// The package row is durable but its mandatory package-specific PNG cover
	// (including the canonical mini-program code) is not ready yet.
	CodeSyncPackageCoverUnavailable Code = 40208
)

// Error is a business error with code and message. Implements error so it
// can be wrapped (`fmt.Errorf("layer X: %w", errx.NewBadRequest(...))`) and
// later recovered with errors.As — see response.Err for the HTTP boundary.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Generic helpers — kept stable for backward compatibility. Module-specific
// segments do NOT add helper constructors here; modules call errx.New
// directly with the appropriate Code so this file does not balloon as new
// segments land. The helpers below are limited to the generic 10xxx range.

func NewBadRequest(msg string) *Error   { return New(CodeBadRequest, msg) }
func NewUnauthorized(msg string) *Error { return New(CodeUnauthorized, msg) }
func NewForbidden(msg string) *Error    { return New(CodeForbidden, msg) }
func NewNotFound(msg string) *Error     { return New(CodeNotFound, msg) }
func NewConflict(msg string) *Error     { return New(CodeConflict, msg) }
func NewInternal(msg string) *Error     { return New(CodeInternal, msg) }
