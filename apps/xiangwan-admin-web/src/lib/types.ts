export type Grant = {
  capability: "super_admin" | "activity_operator" | "onsite_checkin" | "finance";
  scope_type: "tenant" | "session";
  scope_id?: string;
};

export type AuthStatus = {
  enabled: boolean;
  authenticated: boolean;
  principal_id?: string;
  expires_at?: string;
  grants: Grant[];
};

export type BrandQuickTag = {
  code: string;
  label: string;
};

export type BrandProfile = {
  configured: boolean;
  lifecycle_status?: "active" | "suspended";
  publication_version: number;
  version: number;
  community_name: string;
  brand_intro: string;
  hero_mode: "text" | "image";
  hero_eyebrow: string;
  hero_subtitle: string;
  hero_image_url: string;
  hero_image_alt: string;
  quick_tags: BrandQuickTag[];
  published_at?: string;
  updated_at?: string;
};

export type Series = {
  id: string;
  title: string;
  status: "draft" | "active" | "archived";
  is_recurring: boolean;
  home_visible: boolean;
  published_instances: number;
  favorite_count: number;
  registration_count: number;
  current_instance_id?: string;
  version: number;
  updated_at: string;
};

export type InstanceDetailBlock =
  | { type: "text"; title: string; body: string }
  | { type: "image"; url: string; caption?: string };

export type QuestionnaireFieldType =
  | "single_choice"
  | "multiple_choice"
  | "single_line"
  | "multiline"
  | "area";

export type QuestionnaireOption = {
  code: string;
  label: string;
};

export type QuestionnaireField = {
  field_id: string;
  code: string;
  type: QuestionnaireFieldType;
  label: string;
  help_text: string;
  required: boolean;
  sort_order: number;
  min_length?: number;
  max_length?: number;
  max_selections?: number;
  options: QuestionnaireOption[];
};

export type InstanceQuestionnaire = {
  configured: boolean;
  questionnaire_version_id?: string;
  instance_id?: string;
  version?: number;
  privacy_purpose?: string;
  privacy_policy_version?: string;
  published_at?: string;
  fields: QuestionnaireField[];
};

export type QuestionnaireTemplate = {
  id: string;
  version_id: string;
  name: string;
  description: string;
  version: number;
  privacy_purpose: string;
  privacy_policy_version: string;
  status: "active" | "archived";
  fields: QuestionnaireField[];
  created_at: string;
  updated_at: string;
};

export type Person = {
  id: string;
  display_name: string;
  headline?: string;
  introduction: string;
  profile_status: "draft" | "published" | "archived";
  moderation_status: "pending" | "approved" | "rejected";
  version: number;
  updated_at: string;
  has_active_binding: boolean;
};

export type InstanceRole = {
  id: string;
  instance_id: string;
  people_profile_id: string;
  display_name: string;
  headline?: string;
  role_code: "host" | "invited_guest" | "course_instructor" | "event_speaker";
  role_status: "active" | "revoked";
  grant_reason: string;
  version: number;
  granted_at: string;
  revoked_at?: string;
};

export type Instance = {
  id: string;
  series_id: string;
  issue_no: number;
  title: string;
  status: "draft" | "pending_publish" | "published" | "completed" | "cancelled" | "archived";
  activity_type?: "ai_roundtable" | "special_event" | "course" | "competition" | "custom";
  cover_image_url?: string;
  detail_blocks?: InstanceDetailBlock[];
  quick_tag_codes: string[];
  publication_version: number;
  presentation_revision?: number;
  scheduled_at?: string;
  version: number;
  published_at?: string;
  updated_at: string;
};

export type Session = {
  id: string;
  instance_id: string;
  title: string;
  status: "draft" | "published" | "cancelled" | "ended" | "archived";
  registration_start_at?: string;
  registration_end_at?: string;
  session_start_at?: string;
  session_end_at?: string;
  capacity?: number;
  group_minimum?: number;
  low_stock_threshold?: number;
  price_cents?: number;
  delivery_mode?: "offline" | "online";
  area?: string;
  venue_name?: string;
  address?: string;
  longitude?: number;
  latitude?: number;
  online_participation_mode?: string;
  online_participation_compliant?: boolean;
  confirmed_registration_count: number;
  sort_order: number;
  version: number;
};

export type InstanceListItem = {
  instance: Instance;
  series_title: string;
  session_count: number;
};

export type InstanceDetail = {
  series: Series;
  instance: Instance;
  sessions: Session[];
  questionnaire: InstanceQuestionnaire;
  copy_lineage?: {
    source_instance_id: string;
    source_instance_version: number;
    source_presentation_revision: number;
    source_questionnaire_version_id?: string;
  };
};

export type SessionReviewStatus = {
  session_id: string;
  status: string;
  public_resource_count: number;
  public_review_available: boolean;
  latest_published_at?: string;
};

export type InstanceReviewStatus = {
  instance_id: string;
  instance_status: string;
  public_review_eligible: boolean;
  public_review_available: boolean;
  instance_review_document_count: number;
  public_session_resource_count: number;
  latest_published_at?: string;
  sessions: SessionReviewStatus[];
};

export type AdminReviewResource = {
  relation_id: string;
  publication_id: string;
  content_id: string;
  tenant_id: string;
  instance_id: string;
  session_id?: string;
  title: string;
  published_at: string;
};

export type Page<T> = {
  items: T[];
  page: number;
  page_size: number;
  total: number;
};

export type Registration = {
  id: string;
  series_id: string;
  instance_id: string;
  session_id: string;
  instance_title: string;
  session_title: string;
  contact_name: string;
  contact_phone: string;
  participation_status: string;
  payment_status?: string;
  refund_status?: string;
  checkin_status: string;
  created_at: string;
  updated_at: string;
};

export type RegistrationDetail = Registration & {
  contact?: { name: string; phone: string };
  price_cents?: number;
  instance_publication_version: number;
  session_version: number;
  confirmed_at?: string;
  cancelled_at?: string;
  cancellation_reason?: string;
  checkin_id?: string;
  checked_in_at?: string;
  successful_refund_cents?: number;
  privacy_policy_version: string;
};

export type RegistrationAnswerSummary = {
  registration_id: string;
  instance_id: string;
  session_id: string;
  questionnaire_version_id: string;
  field_id: string;
  field_code: string;
  field_type: string;
  field_label: string;
  required: boolean;
  sort_order: number;
  answered: boolean;
  value_count: number;
  created_at: string;
};

export type RegistrationAnswerDetailSet = {
  registration_id: string;
  instance_id: string;
  session_id: string;
  items: Array<{
    questionnaire_version_id: string;
    field_id: string;
    field_code: string;
    field_type: string;
    field_label: string;
    required: boolean;
    sort_order: number;
    options: Array<{ code: string; label: string }>;
    answer_values: string[];
    created_at: string;
  }>;
};

export type AuditEventPage = {
  items: Array<{
    id: string;
    actor_id: string;
    action_code: string;
    target_type: string;
    target_id: string;
    occurred_at: string;
  }>;
  page: number;
  page_size: number;
  total: number;
  as_of: string;
};

export type CouponCorrectionPage = {
  items: Array<{
    entry_id: string;
    coupon_id: string;
    face_value_cents: string;
    source_checkin_event_id: string;
    related_entry_type: string;
    order_id?: string;
    registration_id?: string;
    recorded_at: string;
    handling_status: "pending" | "processing" | "resolved";
    handling_version: number;
    evidence_kind?: "financial" | "entitlement";
    adjustment_cents: string;
  }>;
  page: number;
  page_size: number;
  total: number;
  as_of: string;
};

export type RefundCaseItem = {
    case_id: string;
    order_id: string;
    registration_id: string;
    instance_id: string;
    session_id: string;
    series_title: string;
    instance_title: string;
    session_title: string;
    status: "pending_manual" | "processing" | "failed" | "refunded" | "rejected";
    reason_code: string;
    requested_refund_cents: string;
    successful_refund_cents: string;
    version: number;
    created_at: string;
    updated_at: string;
};

export type RefundQueuePage = {
  items: RefundCaseItem[];
  status: "pending_manual" | "processing" | "failed";
  next_cursor: string;
};

export type RefundCaseDetail = {
  case: RefundCaseItem;
  events: Array<{
    sequence: number;
    type: "processing_started" | "refund_completed" | "refund_failed" | "refund_rejected";
    from_status: RefundCaseItem["status"];
    to_status: RefundCaseItem["status"];
    successful_refund_cents: string;
    resulting_refund_version: number;
    occurred_at: string;
  }>;
};

export type OrderListItem = {
  order_id: string;
  registration_id: string;
  instance_id: string;
  session_id: string;
  series_title: string;
  instance_title: string;
  session_title: string;
  payment_status: "pending" | "unknown" | "paid_confirmed" | "settled_zero" | "closed_unpaid";
  original_price_cents: string;
  discount_cents: string;
  payable_cents: string;
  actual_paid_cents: string | null;
  refund_case_id: string | null;
  refund_status: string;
  requested_refund_cents: string;
  successful_refund_cents: string;
  created_at: string;
  updated_at: string;
  paid_at: string | null;
  closed_at: string | null;
};

export type OrderListPage = {
  items: OrderListItem[];
  status: "all" | "pending" | "unknown" | "paid_confirmed" | "settled_zero" | "closed_unpaid";
  next_cursor: string;
};

export type Verification = {
  decision: string;
  verification_attempt_id: string;
  series_id: string;
  instance_id: string;
  session_id: string;
  can_record: boolean;
  registration_id?: string;
  credential_id?: string;
  checkin_id?: string;
  occurred_at: string;
  replayed: boolean;
};

export type Checkin = {
  checkin_id: string;
  registration_id: string;
  series_id: string;
  instance_id: string;
  session_id: string;
  status: string;
  checked_in_at: string;
  duplicate: boolean;
};

export type CheckinTarget = {
  series_id: string;
  instance_id: string;
  session_id: string;
  series_title: string;
  instance_title: string;
  session_title: string;
  session_start_at: string;
  venue_name: string;
  confirmed_registration_count: number;
  checked_in_registration_count: number;
  capacity: number;
};
