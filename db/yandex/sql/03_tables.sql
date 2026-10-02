-- =============================================================================
-- 03_tables.sql — application public tables (columns only).
--
-- Source: supabase/migrations/00000000000002_baseline_tables.sql.
--
-- IMPORT-FRIENDLY SPLIT:
--   * This file creates tables with columns + column defaults + NOT NULL only.
--   * PRIMARY KEY / UNIQUE / CHECK / FOREIGN KEY constraints live in
--     06_indexes_constraints.sql so a bulk PROD->Yandex data load can run
--     before constraints/indexes are validated.
--
-- CLEANING APPLIED vs the Supabase migration:
--   * DEFAULT extensions.uuid_generate_v4()  ->  DEFAULT gen_random_uuid()
--     (no schema-qualified extension calls; gen_random_uuid() is from pgcrypto,
--      already enabled on the Yandex cluster — see 07_SCHEMA_BUILD_REPORT.md §10).
--   * No CREATE EXTENSION. No Supabase-internal objects.
--
-- 40 tables. Order = original migration levels (independent -> dependent), kept
-- for readability only (no FKs here, so order is not load-critical).
-- =============================================================================

-- ----- LEVEL 0: independent tables -----------------------------------------

CREATE TABLE IF NOT EXISTS public.roles (
    code text NOT NULL,
    name text NOT NULL,
    allowed_pages jsonb NOT NULL DEFAULT '[]'::jsonb,
    is_system_role boolean NOT NULL DEFAULT false,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    color text DEFAULT 'default'::text
);

CREATE TABLE IF NOT EXISTS public.units (
    code text NOT NULL,
    name text NOT NULL,
    description text,
    category text,
    sort_order integer DEFAULT 0,
    is_active boolean DEFAULT true,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.construction_scopes (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.tender_statuses (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.markup_parameters (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    key text NOT NULL,
    label text NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    order_num integer NOT NULL DEFAULT 0,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    default_value numeric NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS public.library_folders (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    type text NOT NULL,
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    parent_id uuid
);

CREATE TABLE IF NOT EXISTS public.notifications (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    type text NOT NULL,
    title text NOT NULL,
    message text NOT NULL,
    related_entity_type text,
    related_entity_id uuid,
    is_read boolean NOT NULL DEFAULT false,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

-- ----- LEVEL 1 --------------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.users (
    id uuid NOT NULL,
    full_name text NOT NULL,
    email text NOT NULL,
    access_status public.access_status_type NOT NULL DEFAULT 'pending'::public.access_status_type,
    approved_by uuid,
    approved_at timestamp with time zone,
    registration_date timestamp with time zone NOT NULL DEFAULT now(),
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    access_enabled boolean DEFAULT true,
    role_code text NOT NULL,
    allowed_pages jsonb DEFAULT '[]'::jsonb,
    tender_deadline_extensions jsonb DEFAULT '[]'::jsonb,
    current_work_mode public.work_mode DEFAULT 'office'::public.work_mode,
    current_work_status public.work_status DEFAULT 'working'::public.work_status
);

CREATE TABLE IF NOT EXISTS public.cost_categories (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    unit text NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.material_names (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    unit text NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.work_names (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    unit text NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

-- ----- LEVEL 2 --------------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.tenders (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    title text NOT NULL,
    description text,
    client_name text NOT NULL,
    tender_number text NOT NULL,
    submission_deadline timestamp with time zone,
    version integer DEFAULT 1,
    area_client numeric,
    area_sp numeric,
    usd_rate numeric,
    eur_rate numeric,
    cny_rate numeric,
    upload_folder text,
    bsm_link text,
    tz_link text,
    qa_form_link text,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    created_by uuid,
    markup_tactic_id uuid,
    apply_subcontract_works_growth boolean DEFAULT true,
    apply_subcontract_materials_growth boolean DEFAULT true,
    housing_class public.housing_class_type,
    construction_scope public.construction_scope_type,
    project_folder_link text,
    is_archived boolean NOT NULL DEFAULT false,
    volume_title text DEFAULT 'Полный объём строительства'::text,
    cached_grand_total numeric NOT NULL DEFAULT 0,
    financial_approved boolean NOT NULL DEFAULT false,
    financial_approved_by uuid,
    financial_approved_at timestamp with time zone,
    -- 0-F2: минимальная revision-модель финансового расчёта
    financial_input_revision bigint NOT NULL DEFAULT 0,
    financial_calculation_revision bigint NOT NULL DEFAULT 0,
    financial_calculation_status text NOT NULL DEFAULT 'calculated'
        CONSTRAINT tenders_financial_calculation_status_check
        CHECK (financial_calculation_status IN ('calculated', 'stale', 'calculating', 'failed')),
    financial_calculation_started_at timestamp with time zone,
    financial_calculated_at timestamp with time zone,
    financial_calculation_error_code text,
    financial_calculation_error_message text,
    CONSTRAINT tenders_financial_revision_order_check
        CHECK (financial_calculation_revision <= financial_input_revision)
);

CREATE TABLE IF NOT EXISTS public.detail_cost_categories (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    cost_category_id uuid NOT NULL,
    location text NOT NULL,
    name text NOT NULL,
    unit text NOT NULL,
    order_num integer DEFAULT 0,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.markup_tactics (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text,
    sequences jsonb NOT NULL DEFAULT '{"мат": [], "раб": [], "суб-мат": [], "суб-раб": [], "мат-комп.": [], "раб-комп.": []}'::jsonb,
    base_costs jsonb NOT NULL DEFAULT '{"мат": 0, "раб": 0, "суб-мат": 0, "суб-раб": 0, "мат-комп.": 0, "раб-комп.": 0}'::jsonb,
    user_id uuid,
    is_global boolean DEFAULT false,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.materials_library (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    material_type public.material_type NOT NULL,
    item_type public.boq_item_type NOT NULL,
    consumption_coefficient numeric DEFAULT 1.0000,
    unit_rate numeric NOT NULL,
    currency_type public.currency_type NOT NULL DEFAULT 'RUB'::public.currency_type,
    delivery_price_type public.delivery_price_type NOT NULL DEFAULT 'в цене'::public.delivery_price_type,
    delivery_amount numeric DEFAULT 0.00000,
    material_name_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    folder_id uuid
);

CREATE TABLE IF NOT EXISTS public.works_library (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    work_name_id uuid NOT NULL,
    item_type public.boq_item_type NOT NULL,
    unit_rate numeric NOT NULL,
    currency_type public.currency_type NOT NULL DEFAULT 'RUB'::public.currency_type,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    folder_id uuid
);

-- ----- LEVEL 3 --------------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.tender_registry (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    title text NOT NULL,
    client_name text NOT NULL,
    construction_scope_id uuid,
    area numeric,
    submission_date timestamp with time zone,
    construction_start_date timestamp with time zone,
    site_visit_photo_url text,
    site_visit_date timestamp with time zone,
    has_tender_package text,
    invitation_date timestamp with time zone,
    status_id uuid,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    created_by uuid,
    chronology text,
    sort_order integer NOT NULL,
    object_address text,
    tender_number text,
    is_archived boolean NOT NULL DEFAULT false,
    chronology_items jsonb DEFAULT '[]'::jsonb,
    tender_package_items jsonb DEFAULT '[]'::jsonb,
    manual_total_cost numeric,
    object_coordinates text,
    commission_date timestamp with time zone,
    dashboard_status text DEFAULT 'calc'::text
);

CREATE TABLE IF NOT EXISTS public.client_positions (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    position_number numeric NOT NULL,
    unit_code text,
    volume numeric,
    client_note text,
    item_no text,
    work_name text NOT NULL,
    manual_volume numeric,
    manual_note text,
    hierarchy_level integer DEFAULT 0,
    is_additional boolean DEFAULT false,
    -- 0-F2 baseline gap found by the disposable-DB acceptance run: these two
    -- columns exist in production and are read by the prepared-redistribution
    -- pipeline (loadPreparedPositions), but were missing from the baseline.
    section_number text,
    position_name text,
    parent_position_id uuid,
    total_material numeric DEFAULT 0,
    total_works numeric DEFAULT 0,
    material_cost_per_unit numeric DEFAULT 0,
    work_cost_per_unit numeric DEFAULT 0,
    total_commercial_material numeric DEFAULT 0,
    total_commercial_work numeric DEFAULT 0,
    total_commercial_material_per_unit numeric DEFAULT 0,
    total_commercial_work_per_unit numeric DEFAULT 0,
    rich_runs jsonb,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.import_sessions (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    user_id uuid,
    tender_id uuid,
    file_name text,
    items_count integer NOT NULL DEFAULT 0,
    positions_snapshot jsonb,
    imported_at timestamp with time zone NOT NULL DEFAULT now(),
    cancelled_at timestamp with time zone,
    cancelled_by uuid,
    created_positions jsonb NOT NULL DEFAULT '[]'::jsonb
);

CREATE TABLE IF NOT EXISTS public.templates (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    detail_cost_category_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    folder_id uuid
);

CREATE TABLE IF NOT EXISTS public.construction_cost_volumes (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    detail_cost_category_id uuid,
    volume numeric DEFAULT 0,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    group_key text,
    notes text
);

CREATE TABLE IF NOT EXISTS public.tender_insurance (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    judicial_pct numeric NOT NULL DEFAULT 0,
    total_pct numeric NOT NULL DEFAULT 0,
    apt_price_m2 numeric NOT NULL DEFAULT 0,
    apt_area numeric NOT NULL DEFAULT 0,
    parking_price_m2 numeric NOT NULL DEFAULT 0,
    parking_area numeric NOT NULL DEFAULT 0,
    storage_price_m2 numeric NOT NULL DEFAULT 0,
    storage_area numeric NOT NULL DEFAULT 0,
    distribute_to_rows boolean NOT NULL DEFAULT true,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.tender_markup_percentage (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    markup_parameter_id uuid NOT NULL,
    value numeric NOT NULL DEFAULT 0,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.tender_notes (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    user_id uuid NOT NULL,
    note_text text NOT NULL DEFAULT ''::text,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.tender_pricing_distribution (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    markup_tactic_id uuid,
    basic_material_base_target text NOT NULL DEFAULT 'material'::text,
    basic_material_markup_target text NOT NULL DEFAULT 'work'::text,
    auxiliary_material_base_target text NOT NULL DEFAULT 'work'::text,
    auxiliary_material_markup_target text NOT NULL DEFAULT 'work'::text,
    work_base_target text NOT NULL DEFAULT 'work'::text,
    work_markup_target text NOT NULL DEFAULT 'work'::text,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    subcontract_basic_material_base_target text NOT NULL DEFAULT 'work'::text,
    subcontract_basic_material_markup_target text NOT NULL DEFAULT 'work'::text,
    subcontract_auxiliary_material_base_target text NOT NULL DEFAULT 'work'::text,
    subcontract_auxiliary_material_markup_target text NOT NULL DEFAULT 'work'::text,
    component_material_base_target text NOT NULL DEFAULT 'work'::text,
    component_material_markup_target text NOT NULL DEFAULT 'work'::text,
    component_work_base_target text NOT NULL DEFAULT 'work'::text,
    component_work_markup_target text NOT NULL DEFAULT 'work'::text
);

CREATE TABLE IF NOT EXISTS public.tender_documents (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    section_type varchar NOT NULL,
    title varchar NOT NULL,
    original_filename varchar,
    content_markdown text NOT NULL,
    file_size bigint,
    upload_date timestamp with time zone DEFAULT now(),
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.subcontract_growth_exclusions (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    detail_cost_category_id uuid NOT NULL,
    exclusion_type text NOT NULL DEFAULT 'works'::text,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.user_tasks (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL,
    tender_id uuid,
    description text NOT NULL,
    task_status public.task_status DEFAULT 'running'::public.task_status,
    completed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

-- ----- LEVEL 4 --------------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.boq_items (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    client_position_id uuid NOT NULL,
    sort_number integer NOT NULL DEFAULT 0,
    boq_item_type public.boq_item_type NOT NULL,
    material_type public.material_type,
    material_name_id uuid,
    work_name_id uuid,
    unit_code text,
    quantity numeric,
    base_quantity numeric,
    consumption_coefficient numeric,
    conversion_coefficient numeric,
    delivery_price_type public.delivery_price_type,
    delivery_amount numeric DEFAULT 0.00000,
    currency_type public.currency_type DEFAULT 'RUB'::public.currency_type,
    total_amount numeric,
    detail_cost_category_id uuid,
    quote_link text,
    -- 1.3: справочные даты источника цены (metadata-only, вне расчёта)
    quote_price_date date,
    quote_valid_until date,
    CONSTRAINT boq_items_quote_dates_check
        CHECK (quote_valid_until IS NULL OR quote_price_date IS NULL
            OR quote_valid_until >= quote_price_date),
    commercial_markup numeric,
    total_commercial_material_cost numeric,
    total_commercial_work_cost numeric,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    parent_work_item_id uuid,
    description text,
    unit_rate numeric DEFAULT 0.00,
    import_session_id uuid
);

CREATE TABLE IF NOT EXISTS public.boq_items_audit (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    boq_item_id uuid NOT NULL,
    operation_type text NOT NULL,
    changed_at timestamp with time zone NOT NULL DEFAULT now(),
    changed_by uuid,
    old_data jsonb,
    new_data jsonb,
    changed_fields text[]
);

CREATE TABLE IF NOT EXISTS public.template_items (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    template_id uuid NOT NULL,
    kind text NOT NULL,
    work_library_id uuid,
    material_library_id uuid,
    parent_work_item_id uuid,
    conversation_coeff numeric,
    position integer NOT NULL DEFAULT 0,
    note text,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    detail_cost_category_id uuid
);

CREATE TABLE IF NOT EXISTS public.user_position_filters (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL,
    tender_id uuid NOT NULL,
    position_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.comparison_notes (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id_1 uuid NOT NULL,
    tender_id_2 uuid NOT NULL,
    cost_category_name text NOT NULL,
    detail_category_key text,
    note text NOT NULL DEFAULT ''::text,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    created_by uuid
);

CREATE TABLE IF NOT EXISTS public.cost_redistribution_results (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    markup_tactic_id uuid NOT NULL,
    boq_item_id uuid NOT NULL,
    original_work_cost numeric,
    deducted_amount numeric NOT NULL DEFAULT 0,
    added_amount numeric NOT NULL DEFAULT 0,
    final_work_cost numeric,
    redistribution_rules jsonb,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    created_by uuid
);

-- Снижение коммерческой стоимости на «Финансовых показателях».
-- Хранятся только параметры (enabled + rules); суммы пересчитываются на загрузке.
CREATE TABLE IF NOT EXISTS public.tender_fi_discounts (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    rules jsonb NOT NULL DEFAULT '[]'::jsonb,
    mode text NOT NULL DEFAULT 'discount',
    zeroed_position_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_by uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

-- ----- LEVEL 5 --------------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.projects (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    client_name text NOT NULL,
    contract_cost numeric NOT NULL DEFAULT 0,
    area numeric,
    construction_end_date date,
    tender_id uuid,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    created_by uuid,
    contract_date date
);

CREATE TABLE IF NOT EXISTS public.project_additional_agreements (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL,
    agreement_date date NOT NULL,
    amount numeric NOT NULL,
    description text,
    agreement_number text,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.project_monthly_completion (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL,
    year integer NOT NULL,
    month integer NOT NULL,
    actual_amount numeric NOT NULL DEFAULT 0,
    forecast_amount numeric,
    note text,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

-- ----- LEVEL 6 --------------------------------------------------------------

CREATE TABLE IF NOT EXISTS public.tender_groups (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    name text NOT NULL,
    color text NOT NULL DEFAULT '#1677ff'::text,
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    quality_level smallint,
    quality_comment text,
    quality_updated_by uuid,
    quality_updated_at timestamp with time zone
);

CREATE TABLE IF NOT EXISTS public.tender_group_members (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    group_id uuid NOT NULL,
    user_id uuid NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.tender_iterations (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    group_id uuid NOT NULL,
    user_id uuid NOT NULL,
    iteration_number integer NOT NULL,
    user_comment text NOT NULL,
    user_amount numeric,
    submitted_at timestamp with time zone NOT NULL DEFAULT now(),
    manager_id uuid,
    manager_comment text,
    manager_responded_at timestamp with time zone,
    approval_status text NOT NULL DEFAULT 'pending'::text,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

-- Этап 2.3: Smart Import Memory — персональные профили сопоставления колонок
-- и подтверждённые номенклатурные aliases (только явные решения пользователя;
-- без financial-полей, workbook bytes и AI prompt/response).
CREATE TABLE IF NOT EXISTS public.boq_import_mapping_profiles (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    normalized_header_signature text NOT NULL CHECK (length(normalized_header_signature) > 0),
    mapping_schema_version integer NOT NULL,
    normalization_version integer NOT NULL,
    mapping jsonb NOT NULL DEFAULT '{}'::jsonb,
    fixed_options jsonb NOT NULL DEFAULT '{}'::jsonb,
    sheet_name_hint text,
    header_row_hint integer,
    is_active boolean NOT NULL DEFAULT true,
    use_count integer NOT NULL DEFAULT 0,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.nomenclature_import_aliases (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL,
    catalog_kind text NOT NULL CHECK (catalog_kind IN ('material', 'work')),
    material_name_id uuid,
    work_name_id uuid,
    normalized_source_text text NOT NULL CHECK (length(btrim(normalized_source_text)) > 0),
    canonical_boq_item_type text NOT NULL CHECK (length(canonical_boq_item_type) > 0),
    normalized_unit_code text,
    detail_cost_category_id uuid,
    normalization_version integer NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    use_count integer NOT NULL DEFAULT 0,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT nomenclature_import_aliases_kind_fk_chk CHECK (
        (catalog_kind = 'material' AND material_name_id IS NOT NULL AND work_name_id IS NULL)
        OR
        (catalog_kind = 'work' AND work_name_id IS NOT NULL AND material_name_id IS NULL)
    )
);

-- Этап 2.5: OpenRouter AI Administration — персистентные настройки AI-фичи.
-- API key OpenRouter здесь НЕ хранится (только server env); без raw
-- prompt/response, без models catalog, без Excel/BOQ и финансовых данных.
CREATE TABLE IF NOT EXISTS public.ai_feature_settings (
    feature_code text NOT NULL CHECK (length(btrim(feature_code)) > 0),
    -- proxy_llm — режим собственного OpenAI-совместимого прокси (2026_08).
    provider text NOT NULL DEFAULT 'openrouter',
    selected_model_id text,
    selected_model_name text,
    selected_model_context_length integer,
    selected_model_max_completion_tokens integer,
    selected_model_prompt_price text,
    selected_model_completion_price text,
    selected_model_expiration_date text,
    selected_model_supported_parameters jsonb NOT NULL DEFAULT '[]'::jsonb,
    prompt_version text NOT NULL DEFAULT 'nomenclature-rerank-v1',
    provider_policy_version text NOT NULL DEFAULT 'openrouter-policy-v1',
    require_zdr boolean NOT NULL DEFAULT true,
    data_collection_policy text NOT NULL DEFAULT 'deny'
        CHECK (data_collection_policy IN ('deny', 'allow')),
    require_parameters boolean NOT NULL DEFAULT true,
    allow_provider_fallbacks boolean NOT NULL DEFAULT false,
    -- Потолок 240 (а не 120): дедлайн LLM-прокси ~190 с (2026_08).
    request_timeout_seconds integer NOT NULL DEFAULT 30,
    max_output_tokens integer NOT NULL DEFAULT 2000
        CHECK (max_output_tokens BETWEEN 128 AND 32000),
    temperature numeric(3, 2) NOT NULL DEFAULT 0
        CHECK (temperature >= 0 AND temperature <= 2),
    candidate_limit integer NOT NULL DEFAULT 20
        CHECK (candidate_limit BETWEEN 1 AND 50),
    max_rows_per_request integer NOT NULL DEFAULT 200
        CHECK (max_rows_per_request BETWEEN 1 AND 200),
    max_concurrency integer NOT NULL DEFAULT 2
        CHECK (max_concurrency BETWEEN 1 AND 3),
    monthly_budget_usd numeric(10, 2)
        CHECK (monthly_budget_usd IS NULL OR monthly_budget_usd >= 0),
    model_test_status text NOT NULL DEFAULT 'required'
        CHECK (model_test_status IN ('required', 'passed', 'failed')),
    model_test_config_hash text,
    model_tested_model_id text,
    model_tested_at timestamp with time zone,
    model_test_latency_ms integer,
    model_test_input_tokens integer,
    model_test_output_tokens integer,
    model_test_estimated_cost text,
    model_test_error_code text,
    -- 2026_08 (proxy_llm): цена модели у прокси неизвестна, поэтому USD-бюджет
    -- дополняется измеримым потолком в токенах; фактическая модель ответа
    -- фиксируется отдельно, а тест протухает по возрасту.
    monthly_token_budget bigint,
    model_test_observed_model text,
    model_test_max_age_hours integer NOT NULL DEFAULT 168,
    -- Аудируемое подтверждение делегирования privacy-политики оператору прокси.
    proxy_privacy_ack_by uuid,
    proxy_privacy_ack_at timestamp with time zone,
    proxy_privacy_ack_policy_version text,
    enabled boolean NOT NULL DEFAULT false,
    needs_review_reason text,
    -- Этап 2.6: controlled rollout (off по умолчанию; general availability НЕТ).
    rollout_mode text NOT NULL DEFAULT 'off',
    rollout_config_version integer NOT NULL DEFAULT 1,
    daily_request_limit integer NOT NULL DEFAULT 20,
    daily_row_limit integer NOT NULL DEFAULT 400,
    request_max_reserved_cost numeric(14, 8) NOT NULL DEFAULT 0.05,
    circuit_failure_threshold integer NOT NULL DEFAULT 3,
    circuit_cooldown_seconds integer NOT NULL DEFAULT 300,
    reservation_timeout_seconds integer NOT NULL DEFAULT 120,
    -- feature/ai-key-ui: UI-ключ OpenRouter — ТОЛЬКО шифротекст (AES-GCM от
    -- серверного JWT-private-key); plaintext в БД запрещён.
    api_key_ciphertext bytea,
    api_key_suffix text,
    api_key_set_at timestamp with time zone,
    api_key_set_by uuid,
    pilot_started_at timestamp with time zone,
    pilot_ended_at timestamp with time zone,
    last_live_evaluation_id uuid,
    updated_by uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT ai_feature_settings_enabled_requires_test CHECK (
        NOT enabled OR (
            selected_model_id IS NOT NULL
            AND model_test_status = 'passed'
            AND model_test_config_hash IS NOT NULL
        )
    ),
    -- Имена/состав совпадают с 2026_07_ai_rollout_controlled.sql (schema
    -- equivalence: baseline == migration chain; DO-блок миграции увидит
    -- существующий conname и пропустит ADD CONSTRAINT).
    CONSTRAINT ai_feature_settings_api_key_suffix_chk
        CHECK (api_key_suffix IS NULL OR length(api_key_suffix) <= 8),
    CONSTRAINT ai_feature_settings_rollout_mode_chk
        CHECK (rollout_mode IN ('off', 'evaluation', 'pilot_individual', 'pilot_bulk')),
    CONSTRAINT ai_feature_settings_rollout_limits_chk CHECK (
        rollout_config_version >= 1
        AND daily_request_limit BETWEEN 1 AND 10000
        AND daily_row_limit BETWEEN 1 AND 1000000
        AND request_max_reserved_cost > 0
        AND circuit_failure_threshold BETWEEN 1 AND 100
        AND circuit_cooldown_seconds BETWEEN 10 AND 86400
        AND reservation_timeout_seconds BETWEEN 10 AND 3600
    ),
    -- Имена/состав совпадают с 2026_08_ai_proxy_llm_mode.sql.
    CONSTRAINT ai_feature_settings_provider_chk
        CHECK (provider IN ('openrouter', 'proxy_llm')),
    CONSTRAINT ai_feature_settings_request_timeout_chk
        CHECK (request_timeout_seconds BETWEEN 5 AND 240),
    -- Резервация обязана переживать вызов с ретраем (2×timeout + запас):
    -- иначе maintenance освободит её в полёте и ReconcileUsage учтёт расход дважды.
    CONSTRAINT ai_feature_settings_reservation_covers_request_chk
        CHECK (reservation_timeout_seconds >= request_timeout_seconds * 2 + 60),
    CONSTRAINT ai_feature_settings_token_budget_chk
        CHECK (monthly_token_budget IS NULL OR monthly_token_budget > 0),
    CONSTRAINT ai_feature_settings_model_test_age_chk
        CHECK (model_test_max_age_hours BETWEEN 1 AND 8760)
);

-- Этап 2.6: пилотная группа, usage ledger, feedback, circuit, evaluation.
-- В ledger/feedback НЕТ raw text/prompt/response/Excel/tender/финансовых данных.
CREATE TABLE IF NOT EXISTS public.ai_pilot_users (
    feature_code text NOT NULL,
    user_id uuid NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    daily_request_limit_override integer
        CHECK (daily_request_limit_override IS NULL OR daily_request_limit_override BETWEEN 1 AND 10000),
    daily_row_limit_override integer
        CHECK (daily_row_limit_override IS NULL OR daily_row_limit_override BETWEEN 1 AND 1000000),
    bulk_confirmation_allowed boolean NOT NULL DEFAULT false,
    expires_at timestamp with time zone,
    added_by uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.ai_usage_requests (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    feature_code text NOT NULL,
    user_id uuid,
    model_id text NOT NULL,
    prompt_version text NOT NULL,
    config_hash text NOT NULL,
    request_hash text NOT NULL,
    rows_count integer NOT NULL CHECK (rows_count >= 0),
    candidates_count integer NOT NULL DEFAULT 0 CHECK (candidates_count >= 0),
    reservation_amount numeric(14, 8) NOT NULL CHECK (reservation_amount >= 0),
    actual_provider_cost numeric(14, 8) CHECK (actual_provider_cost IS NULL OR actual_provider_cost >= 0),
    estimated_cost numeric(14, 8) CHECK (estimated_cost IS NULL OR estimated_cost >= 0),
    -- unpriced_reservation (2026_08): режим без каталога цен — резерв плоский,
    -- выдавать его за catalog_estimate значило бы соврать в отчётах о расходе.
    cost_source text,
    prompt_tokens integer NOT NULL DEFAULT 0 CHECK (prompt_tokens >= 0),
    completion_tokens integer NOT NULL DEFAULT 0 CHECK (completion_tokens >= 0),
    total_tokens integer NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),
    provider_outcome text,
    request_status text NOT NULL DEFAULT 'reserved'
        CHECK (request_status IN ('reserved', 'completed', 'released', 'failed')),
    reservation_underestimate boolean NOT NULL DEFAULT false,
    latency_ms integer,
    reservation_expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    completed_at timestamp with time zone,
    -- 2026_08 (proxy_llm): фактическая модель ответа ловит дрейф на стороне
    -- оператора; upstream_request_id — мост к счёту OpenRouter; reserved_tokens
    -- держит запросы «в полёте» внутри токенного потолка.
    observed_model text,
    upstream_request_id text,
    reserved_tokens bigint,
    CONSTRAINT ai_usage_requests_cost_source_chk
        CHECK (cost_source IS NULL OR cost_source IN
            ('provider_reported', 'catalog_estimate', 'unpriced_reservation'))
);

CREATE TABLE IF NOT EXISTS public.ai_row_feedback (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    request_id uuid NOT NULL,
    user_id uuid,
    row_context_hash text NOT NULL CHECK (length(row_context_hash) BETWEEN 8 AND 128),
    confidence text NOT NULL DEFAULT '',
    deterministic_top_catalog_id text,
    ai_selected_catalog_id text,
    final_selected_catalog_id text,
    outcome text CHECK (outcome IS NULL OR outcome IN ('accepted', 'changed', 'manual', 'abstained', 'unresolved')),
    selection_source text,
    imported_successfully boolean NOT NULL DEFAULT false,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    completed_at timestamp with time zone
);

CREATE TABLE IF NOT EXISTS public.ai_circuit_state (
    feature_code text NOT NULL,
    circuit_state text NOT NULL DEFAULT 'closed'
        CHECK (circuit_state IN ('closed', 'open', 'half_open')),
    consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    open_until timestamp with time zone,
    last_failure_code text,
    last_success_at timestamp with time zone,
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.ai_evaluation_summaries (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    feature_code text NOT NULL,
    eval_mode text NOT NULL CHECK (eval_mode IN ('deterministic', 'mock', 'live')),
    dataset_kind text NOT NULL CHECK (dataset_kind IN ('synthetic', 'approved_aliases')),
    dataset_hash text NOT NULL,
    dataset_size integer NOT NULL CHECK (dataset_size >= 0),
    model_id text NOT NULL,
    prompt_version text NOT NULL,
    config_hash text NOT NULL,
    metrics jsonb NOT NULL DEFAULT '{}'::jsonb,
    gates_passed boolean NOT NULL DEFAULT false,
    gate_details jsonb NOT NULL DEFAULT '{}'::jsonb,
    executed_by uuid,
    executed_at timestamp with time zone NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS public.quality_acknowledgements (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    rule_code text NOT NULL,
    entity_id uuid NOT NULL,
    fingerprint text NOT NULL,
    verdict text NOT NULL,
    note text,
    created_by uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

-- Конвейер проверки: прогоны каталога правил и материализованное состояние находок
-- (см. db/yandex/incremental/2026_09_verification_findings.sql).
CREATE TABLE IF NOT EXISTS public.verification_runs (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    trigger_source text NOT NULL,
    input_revision bigint NOT NULL DEFAULT 0,
    catalog_hash text NOT NULL,
    rules_executed integer NOT NULL DEFAULT 0,
    rule_errors jsonb NOT NULL DEFAULT '[]'::jsonb,
    findings_total integer NOT NULL DEFAULT 0,
    opened_count integer NOT NULL DEFAULT 0,
    reopened_count integer NOT NULL DEFAULT 0,
    resolved_count integer NOT NULL DEFAULT 0,
    started_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone NOT NULL DEFAULT now(),
    duration_ms integer NOT NULL DEFAULT 0,
    triggered_by uuid
);
CREATE TABLE IF NOT EXISTS public.verification_findings (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    source text NOT NULL DEFAULT 'rules',
    rule_code text NOT NULL,
    entity_type text NOT NULL,
    entity_id uuid NOT NULL,
    client_position_id uuid,
    position_number numeric,
    item_no text,
    severity text NOT NULL,
    detail text NOT NULL,
    money_delta numeric,
    fingerprint text NOT NULL,
    first_seen_run_id uuid,
    last_seen_run_id uuid,
    first_seen_at timestamp with time zone NOT NULL DEFAULT now(),
    last_seen_at timestamp with time zone NOT NULL DEFAULT now(),
    resolved_at timestamp with time zone,
    reopen_count integer NOT NULL DEFAULT 0,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS public.verification_finding_events (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    finding_id uuid NOT NULL,
    event_type text NOT NULL,
    fingerprint text,
    run_id uuid,
    actor_user_id uuid,
    source text NOT NULL DEFAULT 'system',
    note text,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);
-- Отметки готовности по разделам ВОР (см. 2026_09_verification_sections.sql).
CREATE TABLE IF NOT EXISTS public.verification_section_states (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    section_key text NOT NULL,
    stage text NOT NULL,
    content_hash text NOT NULL,
    hash_version smallint NOT NULL DEFAULT 1,
    note text,
    marked_by uuid,
    marked_at timestamp with time zone NOT NULL DEFAULT now(),
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS public.verification_section_events (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    section_key text NOT NULL,
    stage text NOT NULL,
    action text NOT NULL,
    content_hash text,
    note text,
    actor_user_id uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);
-- Эталонные диапазоны удельных показателей по классу жилья
-- (см. 2026_09_benchmark_ranges.sql).
CREATE TABLE IF NOT EXISTS public.benchmark_ranges (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    metric_kind text NOT NULL,
    level text NOT NULL,
    cost_category_id uuid,
    detail_cost_category_id uuid,
    housing_class public.housing_class_type,
    construction_scope public.construction_scope_type,
    min_value numeric,
    max_value numeric,
    note text,
    is_active boolean NOT NULL DEFAULT true,
    created_by uuid,
    updated_by uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);
-- Краткая выжимка по тендеру для руководства (см. 2026_09_tender_briefs.sql).
CREATE TABLE IF NOT EXISTS public.tender_briefs (
    tender_id uuid NOT NULL,
    summary_text text NOT NULL DEFAULT '',
    fact_category_ids uuid[],
    updated_by uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

-- Рассылка замечаний проверки в Telegram (см. 2026_09_verification_telegram.sql).
CREATE TABLE IF NOT EXISTS public.telegram_links (
    user_id uuid NOT NULL,
    chat_id bigint NOT NULL,
    telegram_username text,
    linked_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.telegram_link_tokens (
    token_hash text NOT NULL,
    user_id uuid NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.telegram_bot_state (
    id smallint NOT NULL DEFAULT 1,
    update_offset bigint NOT NULL DEFAULT 0,
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.verification_notifications (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid NOT NULL,
    recipient_user_id uuid NOT NULL,
    created_by uuid,
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamp with time zone NOT NULL DEFAULT now(),
    last_error text,
    telegram_message_id bigint,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    sent_at timestamp with time zone
);

CREATE TABLE IF NOT EXISTS public.verification_notification_items (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    notification_id uuid NOT NULL,
    finding_id uuid NOT NULL,
    recipient_user_id uuid NOT NULL,
    fingerprint text NOT NULL,
    resolver text NOT NULL,
    dedup_key text,
    verdict text,
    verdict_at timestamp with time zone
);

-- Журнал напоминаний «Перечня тендеров», выданных внешнему боту через API
-- (см. 2026_10_telegram_tender_bot.sql, 2026_10_tender_registry_api.sql).
CREATE TABLE IF NOT EXISTS public.telegram_tender_reminders (
    chat_id bigint NOT NULL,
    registry_id uuid NOT NULL,
    kind text NOT NULL,
    due_on date NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    claimed_until timestamp with time zone,
    last_error text,
    telegram_message_id bigint,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    sent_at timestamp with time zone
);

-- Машинный доступ к API (страница «Настройки → Доступ к API»).
-- Секрет ключа в БД не хранится: только SHA-256 хеш и префикс для опознания.
CREATE TABLE IF NOT EXISTS public.api_keys (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    key_prefix text NOT NULL,
    key_hash text NOT NULL,
    scopes text[] NOT NULL DEFAULT '{}',
    allowed_tender_ids uuid[],
    expires_at timestamp with time zone,
    revoked_at timestamp with time zone,
    revoked_by uuid,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    created_by uuid NOT NULL
);
CREATE TABLE IF NOT EXISTS public.api_access_settings (
    id boolean NOT NULL DEFAULT true,
    archive_search_enabled boolean NOT NULL DEFAULT true,
    archive_read_enabled boolean NOT NULL DEFAULT true,
    archive_suggest_enabled boolean NOT NULL DEFAULT true,
    archive_compose_enabled boolean NOT NULL DEFAULT true,
    max_search_limit integer NOT NULL DEFAULT 200,
    max_candidate_limit integer NOT NULL DEFAULT 4000,
    max_suggest_queries integer NOT NULL DEFAULT 100,
    rate_limit_per_minute integer NOT NULL DEFAULT 120,
    call_log_retention_days integer NOT NULL DEFAULT 30,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_by uuid
);
CREATE TABLE IF NOT EXISTS public.api_call_log (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    api_key_id uuid,
    user_id uuid,
    method text NOT NULL,
    path text NOT NULL,
    status integer NOT NULL,
    duration_ms integer NOT NULL DEFAULT 0,
    error_code text,
    items_affected integer,
    dry_run boolean,
    called_at timestamp with time zone NOT NULL DEFAULT now()
);

-- ИИ-разбор находок проверки данных (см. 2026_09_verification_triage_ai.sql).
CREATE TABLE IF NOT EXISTS public.verification_ai_settings (
    id smallint NOT NULL DEFAULT 1,
    enabled boolean NOT NULL DEFAULT false,
    model_id text,
    allowed_tender_ids uuid[],
    max_findings_per_run integer NOT NULL DEFAULT 200,
    batch_size integer NOT NULL DEFAULT 8,
    max_output_tokens integer NOT NULL DEFAULT 3000,
    request_timeout_seconds integer NOT NULL DEFAULT 120,
    monthly_token_budget bigint NOT NULL DEFAULT 10000000,
    daily_request_limit integer NOT NULL DEFAULT 1000,
    last_test_at timestamp with time zone,
    last_test_model_id text,
    last_test_status text,
    last_test_error text,
    last_test_latency_ms integer,
    updated_by uuid,
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.verification_ai_requests (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    tender_id uuid,
    trigger_source text NOT NULL,
    status text NOT NULL,
    model_id text NOT NULL,
    prompt_version text NOT NULL,
    findings_count integer NOT NULL DEFAULT 0,
    assessed_count integer NOT NULL DEFAULT 0,
    prompt_tokens integer NOT NULL DEFAULT 0,
    completion_tokens integer NOT NULL DEFAULT 0,
    total_tokens integer NOT NULL DEFAULT 0,
    cost numeric(14, 8),
    latency_ms integer NOT NULL DEFAULT 0,
    error_code text,
    created_by uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS public.verification_ai_assessments (
    finding_id uuid NOT NULL,
    tender_id uuid NOT NULL,
    rule_code text NOT NULL,
    fingerprint text NOT NULL,
    prompt_version text NOT NULL,
    model_id text NOT NULL,
    label text NOT NULL,
    reason text NOT NULL DEFAULT '',
    evidence jsonb NOT NULL DEFAULT '[]'::jsonb,
    downgraded boolean NOT NULL DEFAULT false,
    request_id uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now()
);
