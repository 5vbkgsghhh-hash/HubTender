-- =============================================================================
-- 06_indexes_constraints.sql — PK / UNIQUE / CHECK / FK / indexes.
--
-- Source: supabase/migrations/00000000000002_baseline_tables.sql (PK + CHECK),
--         00000000000003_baseline_foreign_keys_and_unique.sql (UNIQUE + FK),
--         00000000000004_baseline_indexes.sql + 00000000000010_*.sql (indexes).
--
-- IMPORT-FRIENDLY ORDERING (apply AFTER a bulk PROD->Yandex data load):
--   1. PRIMARY KEY  (all tables)
--   2. UNIQUE
--   3. CHECK
--   4. FOREIGN KEY  (incl. FKs to the auth.users bridge — Option A, see
--                    01_auth_compat_or_app_auth.sql / 03_SCHEMA_STRATEGY.md §5)
--   5. Non-constraint indexes
--
-- Plain ALTER ... ADD CONSTRAINT (matches the source migrations verbatim). This
-- file is NOT idempotent on its own — it targets an EMPTY database guaranteed by
-- the green YANDEX preflight gate (docs/yandex-migration/06_YANDEX_PREFLIGHT.md).
-- Indexes use IF NOT EXISTS (as in the source migrations).
-- No CREATE EXTENSION / CREATE ROLE / Supabase roles.
-- =============================================================================

-- ===========================================================================
-- 1. PRIMARY KEYS
-- ===========================================================================
ALTER TABLE public.roles                         ADD CONSTRAINT roles_pkey PRIMARY KEY (code);
ALTER TABLE public.units                         ADD CONSTRAINT units_pkey PRIMARY KEY (code);
ALTER TABLE public.construction_scopes           ADD CONSTRAINT construction_scopes_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_statuses               ADD CONSTRAINT tender_statuses_pkey PRIMARY KEY (id);
ALTER TABLE public.markup_parameters             ADD CONSTRAINT markup_parameters_pkey PRIMARY KEY (id);
ALTER TABLE public.library_folders               ADD CONSTRAINT library_folders_pkey PRIMARY KEY (id);
ALTER TABLE public.notifications                 ADD CONSTRAINT notifications_pkey PRIMARY KEY (id);
ALTER TABLE public.users                         ADD CONSTRAINT users_pkey PRIMARY KEY (id);
ALTER TABLE public.cost_categories               ADD CONSTRAINT cost_categories_pkey PRIMARY KEY (id);
ALTER TABLE public.material_names                ADD CONSTRAINT material_names_pkey PRIMARY KEY (id);
ALTER TABLE public.work_names                    ADD CONSTRAINT work_names_pkey PRIMARY KEY (id);
ALTER TABLE public.tenders                       ADD CONSTRAINT tenders_pkey PRIMARY KEY (id);
ALTER TABLE public.detail_cost_categories        ADD CONSTRAINT detail_cost_categories_pkey PRIMARY KEY (id);
ALTER TABLE public.markup_tactics                ADD CONSTRAINT markup_tactics_pkey PRIMARY KEY (id);
ALTER TABLE public.materials_library             ADD CONSTRAINT materials_library_pkey PRIMARY KEY (id);
ALTER TABLE public.works_library                 ADD CONSTRAINT works_library_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_registry               ADD CONSTRAINT tender_registry_pkey PRIMARY KEY (id);
ALTER TABLE public.client_positions              ADD CONSTRAINT client_positions_pkey PRIMARY KEY (id);
ALTER TABLE public.import_sessions               ADD CONSTRAINT import_sessions_pkey PRIMARY KEY (id);
ALTER TABLE public.templates                     ADD CONSTRAINT templates_pkey PRIMARY KEY (id);
ALTER TABLE public.construction_cost_volumes     ADD CONSTRAINT construction_cost_volumes_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_insurance              ADD CONSTRAINT tender_insurance_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_markup_percentage      ADD CONSTRAINT tender_markup_percentage_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_notes                  ADD CONSTRAINT tender_notes_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_pricing_distribution   ADD CONSTRAINT tender_pricing_distribution_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_documents              ADD CONSTRAINT tender_documents_pkey PRIMARY KEY (id);
ALTER TABLE public.subcontract_growth_exclusions ADD CONSTRAINT subcontract_growth_exclusions_pkey PRIMARY KEY (id);
ALTER TABLE public.user_tasks                    ADD CONSTRAINT user_tasks_pkey PRIMARY KEY (id);
ALTER TABLE public.boq_items                     ADD CONSTRAINT boq_items_pkey PRIMARY KEY (id);
ALTER TABLE public.boq_items_audit               ADD CONSTRAINT boq_items_audit_pkey PRIMARY KEY (id);
ALTER TABLE public.template_items                ADD CONSTRAINT template_items_pkey PRIMARY KEY (id);
ALTER TABLE public.user_position_filters         ADD CONSTRAINT user_position_filters_pkey PRIMARY KEY (id);
ALTER TABLE public.comparison_notes              ADD CONSTRAINT comparison_notes_pkey PRIMARY KEY (id);
ALTER TABLE public.cost_redistribution_results   ADD CONSTRAINT cost_redistribution_results_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_fi_discounts           ADD CONSTRAINT tender_fi_discounts_pkey PRIMARY KEY (id);
ALTER TABLE public.projects                      ADD CONSTRAINT projects_pkey PRIMARY KEY (id);
ALTER TABLE public.project_additional_agreements ADD CONSTRAINT project_additional_agreements_pkey PRIMARY KEY (id);
ALTER TABLE public.project_monthly_completion    ADD CONSTRAINT project_monthly_completion_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_groups                 ADD CONSTRAINT tender_groups_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_group_members          ADD CONSTRAINT tender_group_members_pkey PRIMARY KEY (id);
ALTER TABLE public.tender_iterations             ADD CONSTRAINT tender_iterations_pkey PRIMARY KEY (id);

-- ===========================================================================
-- 2. UNIQUE constraints
-- ===========================================================================
ALTER TABLE public.roles ADD CONSTRAINT roles_name_key UNIQUE (name);
ALTER TABLE public.users ADD CONSTRAINT users_email_key UNIQUE (email);
ALTER TABLE public.construction_scopes ADD CONSTRAINT construction_scopes_name_key UNIQUE (name);
ALTER TABLE public.tender_statuses ADD CONSTRAINT tender_statuses_name_key UNIQUE (name);
ALTER TABLE public.markup_parameters ADD CONSTRAINT markup_parameters_key_key UNIQUE (key);
ALTER TABLE public.tenders ADD CONSTRAINT tenders_tender_number_version_key UNIQUE (tender_number, version);
ALTER TABLE public.tender_groups ADD CONSTRAINT tender_groups_tender_id_name_key UNIQUE (tender_id, name);
ALTER TABLE public.tender_group_members ADD CONSTRAINT tender_group_members_group_id_user_id_key UNIQUE (group_id, user_id);
ALTER TABLE public.tender_iterations ADD CONSTRAINT tender_iterations_group_id_user_id_iteration_number_key UNIQUE (group_id, user_id, iteration_number);
ALTER TABLE public.tender_insurance ADD CONSTRAINT tender_insurance_tender_id_unique UNIQUE (tender_id);
ALTER TABLE public.tender_markup_percentage ADD CONSTRAINT tender_markup_percentage_unique UNIQUE (tender_id, markup_parameter_id);
ALTER TABLE public.tender_notes ADD CONSTRAINT tender_notes_tender_id_user_id_key UNIQUE (tender_id, user_id);
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_tender_id_markup_tactic_id_key UNIQUE (tender_id, markup_tactic_id);
ALTER TABLE public.subcontract_growth_exclusions ADD CONSTRAINT subcontract_growth_exclusions_unique UNIQUE (tender_id, detail_cost_category_id, exclusion_type);
ALTER TABLE public.project_monthly_completion ADD CONSTRAINT project_monthly_completion_unique UNIQUE (project_id, year, month);
ALTER TABLE public.comparison_notes ADD CONSTRAINT comparison_notes_tender_id_1_tender_id_2_cost_category_name_key UNIQUE (tender_id_1, tender_id_2, cost_category_name, detail_category_key);
ALTER TABLE public.tender_documents ADD CONSTRAINT unique_tender_section_file UNIQUE (tender_id, section_type, original_filename);
ALTER TABLE public.cost_redistribution_results ADD CONSTRAINT uq_cost_redistribution_results_tender_tactic_boq UNIQUE (tender_id, markup_tactic_id, boq_item_id);
-- Одна строка настроек снижения на тендер: на этот UNIQUE опирается UPSERT в FIDiscountsRepo.
ALTER TABLE public.tender_fi_discounts ADD CONSTRAINT uq_tender_fi_discounts_tender UNIQUE (tender_id);
ALTER TABLE public.user_position_filters ADD CONSTRAINT unique_user_tender_position UNIQUE (user_id, tender_id, position_id);

-- ===========================================================================
-- 3. CHECK constraints
-- ===========================================================================
ALTER TABLE public.roles ADD CONSTRAINT roles_code_format CHECK (code ~ '^[a-z_]+$'::text);
ALTER TABLE public.roles ADD CONSTRAINT roles_name_not_empty CHECK (btrim(name) <> ''::text);
ALTER TABLE public.markup_parameters ADD CONSTRAINT markup_parameters_default_value_range CHECK (default_value >= (0)::numeric AND default_value <= 999.99);
ALTER TABLE public.markup_parameters ADD CONSTRAINT markup_parameters_key_check CHECK (key ~ '^[a-z0-9_]+$'::text);
ALTER TABLE public.library_folders ADD CONSTRAINT library_folders_type_check CHECK (type = ANY (ARRAY['works'::text, 'materials'::text, 'templates'::text]));
ALTER TABLE public.notifications ADD CONSTRAINT notifications_type_check CHECK (type = ANY (ARRAY['success'::text, 'info'::text, 'warning'::text, 'pending'::text]));
ALTER TABLE public.users ADD CONSTRAINT users_email_check CHECK (email ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'::text);
ALTER TABLE public.users ADD CONSTRAINT users_full_name_check CHECK (btrim(full_name) <> ''::text);
ALTER TABLE public.tender_registry ADD CONSTRAINT tender_registry_dashboard_status_check CHECK (dashboard_status = ANY (ARRAY['calc'::text, 'sent'::text, 'waiting_pd'::text, 'archive'::text]) OR dashboard_status IS NULL);
ALTER TABLE public.construction_cost_volumes ADD CONSTRAINT check_volume_type CHECK ((detail_cost_category_id IS NOT NULL AND group_key IS NULL) OR (detail_cost_category_id IS NULL AND group_key IS NOT NULL));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_basic_material_base_target_check CHECK (basic_material_base_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_basic_material_markup_target_check CHECK (basic_material_markup_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_auxiliary_material_base_target_check CHECK (auxiliary_material_base_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_auxiliary_material_markup_target_check CHECK (auxiliary_material_markup_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_work_base_target_check CHECK (work_base_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_work_markup_target_check CHECK (work_markup_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_subcontract_basic_material_base_target_check CHECK (subcontract_basic_material_base_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_subcontract_basic_material_markup_target_check CHECK (subcontract_basic_material_markup_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_component_material_base_target_check CHECK (component_material_base_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_component_material_markup_target_check CHECK (component_material_markup_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_component_work_base_target_check CHECK (component_work_base_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_component_work_markup_target_check CHECK (component_work_markup_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_subcontract_auxiliary_material_base_target_check CHECK (subcontract_auxiliary_material_base_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_subcontract_auxiliary_material_markup_target_check CHECK (subcontract_auxiliary_material_markup_target = ANY (ARRAY['material'::text, 'work'::text]));
ALTER TABLE public.subcontract_growth_exclusions ADD CONSTRAINT subcontract_growth_exclusions_exclusion_type_check CHECK (exclusion_type = ANY (ARRAY['works'::text, 'materials'::text]));
ALTER TABLE public.user_tasks ADD CONSTRAINT user_tasks_description_check CHECK (btrim(description) <> ''::text);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_material_check CHECK ((boq_item_type = ANY (ARRAY['мат'::public.boq_item_type, 'суб-мат'::public.boq_item_type, 'мат-комп.'::public.boq_item_type]) AND material_name_id IS NOT NULL AND work_name_id IS NULL) OR (boq_item_type = ANY (ARRAY['раб'::public.boq_item_type, 'суб-раб'::public.boq_item_type, 'раб-комп.'::public.boq_item_type]) AND work_name_id IS NOT NULL AND material_name_id IS NULL));
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_parent_work_check CHECK ((boq_item_type = ANY (ARRAY['мат'::public.boq_item_type, 'суб-мат'::public.boq_item_type, 'мат-комп.'::public.boq_item_type])) OR (boq_item_type = ANY (ARRAY['раб'::public.boq_item_type, 'суб-раб'::public.boq_item_type, 'раб-комп.'::public.boq_item_type]) AND parent_work_item_id IS NULL));
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_delivery_amount_check CHECK ((delivery_price_type = 'суммой'::public.delivery_price_type AND delivery_amount IS NOT NULL) OR (delivery_price_type = ANY (ARRAY['в цене'::public.delivery_price_type, 'не в цене'::public.delivery_price_type]) AND (delivery_amount IS NULL OR delivery_amount = (0)::numeric)) OR delivery_price_type IS NULL);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_quantity_positive CHECK (quantity IS NULL OR quantity > (0)::numeric);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_base_quantity_positive CHECK (base_quantity IS NULL OR base_quantity > (0)::numeric);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_consumption_coefficient_positive CHECK (consumption_coefficient IS NULL OR consumption_coefficient > (0)::numeric);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_conversion_coefficient_positive CHECK (conversion_coefficient IS NULL OR conversion_coefficient > (0)::numeric);
ALTER TABLE public.boq_items_audit ADD CONSTRAINT boq_items_audit_operation_type_check CHECK (operation_type = ANY (ARRAY['INSERT'::text, 'UPDATE'::text, 'DELETE'::text]));
ALTER TABLE public.boq_items_audit ADD CONSTRAINT audit_data_check CHECK ((operation_type = 'INSERT'::text AND old_data IS NULL AND new_data IS NOT NULL) OR (operation_type = 'UPDATE'::text AND old_data IS NOT NULL AND new_data IS NOT NULL) OR (operation_type = 'DELETE'::text AND old_data IS NOT NULL AND new_data IS NULL));
ALTER TABLE public.template_items ADD CONSTRAINT template_items_kind_check CHECK (kind = ANY (ARRAY['work'::text, 'material'::text]));
ALTER TABLE public.template_items ADD CONSTRAINT template_items_work_logic_check CHECK (kind <> 'work'::text OR (work_library_id IS NOT NULL AND material_library_id IS NULL AND parent_work_item_id IS NULL AND conversation_coeff IS NULL));
ALTER TABLE public.template_items ADD CONSTRAINT template_items_material_logic_check CHECK (kind <> 'material'::text OR (work_library_id IS NULL AND material_library_id IS NOT NULL AND ((parent_work_item_id IS NULL AND conversation_coeff IS NULL) OR (parent_work_item_id IS NOT NULL AND conversation_coeff IS NOT NULL))));
ALTER TABLE public.project_monthly_completion ADD CONSTRAINT project_monthly_completion_month_check CHECK (month >= 1 AND month <= 12);
ALTER TABLE public.tender_groups ADD CONSTRAINT tender_groups_quality_level_check CHECK (quality_level IS NULL OR (quality_level >= 1 AND quality_level <= 10));
ALTER TABLE public.tender_iterations ADD CONSTRAINT tender_iterations_approval_status_check CHECK (approval_status = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text]));
ALTER TABLE public.tender_iterations ADD CONSTRAINT tender_iterations_manager_required_for_approved CHECK (approval_status <> 'approved'::text OR manager_id IS NOT NULL);
ALTER TABLE public.tender_iterations ADD CONSTRAINT tender_iterations_manager_required_for_rejected CHECK (approval_status <> 'rejected'::text OR manager_id IS NOT NULL);
ALTER TABLE public.tender_iterations ADD CONSTRAINT tender_iterations_response_date_required_for_approved CHECK (approval_status <> 'approved'::text OR manager_responded_at IS NOT NULL);
ALTER TABLE public.tender_iterations ADD CONSTRAINT tender_iterations_response_date_required_for_rejected CHECK (approval_status <> 'rejected'::text OR manager_responded_at IS NOT NULL);

-- ===========================================================================
-- 4. FOREIGN KEYS
--    FKs to auth.users(id) are KEPT (Option A bridge). When Option B
--    (app_auth) lands in a later stage, these are rewritten to public.users /
--    an app identity table — see docs/yandex-migration/03_SCHEMA_STRATEGY.md §5.
-- ===========================================================================

-- users (self / auth bridge / roles)
ALTER TABLE public.users ADD CONSTRAINT users_id_fkey FOREIGN KEY (id) REFERENCES auth.users(id) ON DELETE CASCADE;
ALTER TABLE public.users ADD CONSTRAINT users_role_code_fkey FOREIGN KEY (role_code) REFERENCES public.roles(code);
ALTER TABLE public.users ADD CONSTRAINT users_approved_by_fkey FOREIGN KEY (approved_by) REFERENCES public.users(id);

-- cost_categories / detail_cost_categories
ALTER TABLE public.cost_categories ADD CONSTRAINT cost_categories_unit_fkey FOREIGN KEY (unit) REFERENCES public.units(code);
ALTER TABLE public.detail_cost_categories ADD CONSTRAINT detail_cost_categories_cost_category_id_fkey FOREIGN KEY (cost_category_id) REFERENCES public.cost_categories(id);
ALTER TABLE public.detail_cost_categories ADD CONSTRAINT detail_cost_categories_unit_fkey FOREIGN KEY (unit) REFERENCES public.units(code);

-- material_names / work_names
ALTER TABLE public.material_names ADD CONSTRAINT material_names_unit_fkey FOREIGN KEY (unit) REFERENCES public.units(code);
ALTER TABLE public.work_names ADD CONSTRAINT work_names_unit_fkey FOREIGN KEY (unit) REFERENCES public.units(code);

-- libraries
ALTER TABLE public.library_folders ADD CONSTRAINT library_folders_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.library_folders(id) ON DELETE CASCADE;
ALTER TABLE public.materials_library ADD CONSTRAINT materials_library_material_name_id_fkey FOREIGN KEY (material_name_id) REFERENCES public.material_names(id);
ALTER TABLE public.materials_library ADD CONSTRAINT materials_library_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.library_folders(id) ON DELETE SET NULL;
ALTER TABLE public.works_library ADD CONSTRAINT works_library_work_name_id_fkey FOREIGN KEY (work_name_id) REFERENCES public.work_names(id);
ALTER TABLE public.works_library ADD CONSTRAINT works_library_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.library_folders(id) ON DELETE SET NULL;

-- tenders
ALTER TABLE public.tenders ADD CONSTRAINT tenders_created_by_fkey FOREIGN KEY (created_by) REFERENCES auth.users(id);
ALTER TABLE public.tenders ADD CONSTRAINT tenders_markup_tactic_id_fkey FOREIGN KEY (markup_tactic_id) REFERENCES public.markup_tactics(id);

-- markup_tactics
ALTER TABLE public.markup_tactics ADD CONSTRAINT markup_tactics_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth.users(id);

-- tender_registry
ALTER TABLE public.tender_registry ADD CONSTRAINT tender_registry_status_id_fkey FOREIGN KEY (status_id) REFERENCES public.tender_statuses(id);
ALTER TABLE public.tender_registry ADD CONSTRAINT tender_registry_construction_scope_id_fkey FOREIGN KEY (construction_scope_id) REFERENCES public.construction_scopes(id);
ALTER TABLE public.tender_registry ADD CONSTRAINT tender_registry_created_by_fkey FOREIGN KEY (created_by) REFERENCES auth.users(id);

-- client_positions
ALTER TABLE public.client_positions ADD CONSTRAINT client_positions_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.client_positions ADD CONSTRAINT client_positions_parent_position_id_fkey FOREIGN KEY (parent_position_id) REFERENCES public.client_positions(id) ON DELETE CASCADE;
ALTER TABLE public.client_positions ADD CONSTRAINT client_positions_unit_code_fkey FOREIGN KEY (unit_code) REFERENCES public.units(code);

-- boq_items and audit
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_client_position_id_fkey FOREIGN KEY (client_position_id) REFERENCES public.client_positions(id) ON DELETE CASCADE;
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_material_name_id_fkey FOREIGN KEY (material_name_id) REFERENCES public.material_names(id);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_work_name_id_fkey FOREIGN KEY (work_name_id) REFERENCES public.work_names(id);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_unit_code_fkey FOREIGN KEY (unit_code) REFERENCES public.units(code);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_detail_cost_category_id_fkey FOREIGN KEY (detail_cost_category_id) REFERENCES public.detail_cost_categories(id);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_parent_work_item_id_fkey FOREIGN KEY (parent_work_item_id) REFERENCES public.boq_items(id) ON DELETE CASCADE;
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_import_session_id_fkey FOREIGN KEY (import_session_id) REFERENCES public.import_sessions(id) ON DELETE SET NULL;
-- NOTE: boq_items_audit.boq_item_id has NO enforced foreign key — by design.
--   * boq_items_audit is historical / audit storage.
--   * Deleted boq_items legitimately keep surviving audit rows (DELETE history).
--   * Live PROD Supabase has NO such FK (only boq_items_audit_changed_by_fkey);
--     an enforced FK here would reject the historical DELETE-audit import.
--   * A NOT VALID FK is NOT used: it still enforces newly inserted rows.
--   * Integrity is verified by an audit-history check (total / orphan /
--     distinct-orphan vs the PROD baseline), not by FK enforcement.
--     See docs/yandex-migration/15_AUDIT_FK_SCHEMA_DECISION.md.
-- Supporting (non-FK) lookup index on the historical reference column:
CREATE INDEX IF NOT EXISTS idx_boq_items_audit_boq_item_id ON public.boq_items_audit(boq_item_id);
ALTER TABLE public.boq_items_audit ADD CONSTRAINT boq_items_audit_changed_by_fkey FOREIGN KEY (changed_by) REFERENCES public.users(id);

-- import_sessions
ALTER TABLE public.import_sessions ADD CONSTRAINT import_sessions_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.import_sessions ADD CONSTRAINT import_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth.users(id);
ALTER TABLE public.import_sessions ADD CONSTRAINT import_sessions_cancelled_by_fkey FOREIGN KEY (cancelled_by) REFERENCES auth.users(id);

-- templates / template_items
ALTER TABLE public.templates ADD CONSTRAINT templates_detail_cost_category_id_fkey FOREIGN KEY (detail_cost_category_id) REFERENCES public.detail_cost_categories(id);
ALTER TABLE public.templates ADD CONSTRAINT templates_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.library_folders(id) ON DELETE SET NULL;
ALTER TABLE public.template_items ADD CONSTRAINT template_items_template_id_fkey FOREIGN KEY (template_id) REFERENCES public.templates(id) ON DELETE CASCADE;
ALTER TABLE public.template_items ADD CONSTRAINT template_items_work_library_id_fkey FOREIGN KEY (work_library_id) REFERENCES public.works_library(id);
ALTER TABLE public.template_items ADD CONSTRAINT template_items_material_library_id_fkey FOREIGN KEY (material_library_id) REFERENCES public.materials_library(id);
ALTER TABLE public.template_items ADD CONSTRAINT template_items_parent_work_item_id_fkey FOREIGN KEY (parent_work_item_id) REFERENCES public.template_items(id) ON DELETE CASCADE;
ALTER TABLE public.template_items ADD CONSTRAINT template_items_detail_cost_category_id_fkey FOREIGN KEY (detail_cost_category_id) REFERENCES public.detail_cost_categories(id);

-- construction_cost_volumes
ALTER TABLE public.construction_cost_volumes ADD CONSTRAINT construction_cost_volumes_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.construction_cost_volumes ADD CONSTRAINT construction_cost_volumes_detail_cost_category_id_fkey FOREIGN KEY (detail_cost_category_id) REFERENCES public.detail_cost_categories(id) ON DELETE CASCADE;

-- tender_insurance / tender_markup / tender_notes / tender_pricing / tender_documents
ALTER TABLE public.tender_insurance ADD CONSTRAINT tender_insurance_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.tender_markup_percentage ADD CONSTRAINT tender_markup_percentage_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.tender_markup_percentage ADD CONSTRAINT tender_markup_percentage_markup_parameter_id_fkey FOREIGN KEY (markup_parameter_id) REFERENCES public.markup_parameters(id) ON DELETE CASCADE;
ALTER TABLE public.tender_notes ADD CONSTRAINT tender_notes_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.tender_notes ADD CONSTRAINT tender_notes_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth.users(id) ON DELETE CASCADE;
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.tender_pricing_distribution ADD CONSTRAINT tender_pricing_distribution_markup_tactic_id_fkey FOREIGN KEY (markup_tactic_id) REFERENCES public.markup_tactics(id) ON DELETE SET NULL;
ALTER TABLE public.tender_documents ADD CONSTRAINT tender_documents_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;

-- subcontract_growth_exclusions / user_tasks / user_position_filters
ALTER TABLE public.subcontract_growth_exclusions ADD CONSTRAINT subcontract_growth_exclusions_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.subcontract_growth_exclusions ADD CONSTRAINT subcontract_growth_exclusions_detail_cost_category_id_fkey FOREIGN KEY (detail_cost_category_id) REFERENCES public.detail_cost_categories(id) ON DELETE CASCADE;
ALTER TABLE public.user_tasks ADD CONSTRAINT user_tasks_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;
ALTER TABLE public.user_tasks ADD CONSTRAINT user_tasks_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE SET NULL;
ALTER TABLE public.user_position_filters ADD CONSTRAINT user_position_filters_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;
ALTER TABLE public.user_position_filters ADD CONSTRAINT user_position_filters_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.user_position_filters ADD CONSTRAINT user_position_filters_position_id_fkey FOREIGN KEY (position_id) REFERENCES public.client_positions(id) ON DELETE CASCADE;

-- comparison_notes / cost_redistribution_results
ALTER TABLE public.comparison_notes ADD CONSTRAINT comparison_notes_tender_id_1_fkey FOREIGN KEY (tender_id_1) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.comparison_notes ADD CONSTRAINT comparison_notes_tender_id_2_fkey FOREIGN KEY (tender_id_2) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.comparison_notes ADD CONSTRAINT comparison_notes_created_by_fkey FOREIGN KEY (created_by) REFERENCES auth.users(id);
ALTER TABLE public.cost_redistribution_results ADD CONSTRAINT cost_redistribution_results_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.cost_redistribution_results ADD CONSTRAINT cost_redistribution_results_markup_tactic_id_fkey FOREIGN KEY (markup_tactic_id) REFERENCES public.markup_tactics(id) ON DELETE CASCADE;
ALTER TABLE public.cost_redistribution_results ADD CONSTRAINT cost_redistribution_results_boq_item_id_fkey FOREIGN KEY (boq_item_id) REFERENCES public.boq_items(id) ON DELETE CASCADE;
ALTER TABLE public.cost_redistribution_results ADD CONSTRAINT cost_redistribution_results_created_by_fkey FOREIGN KEY (created_by) REFERENCES auth.users(id);

-- tender_fi_discounts (снижение на «Финансовых показателях»)
ALTER TABLE public.tender_fi_discounts ADD CONSTRAINT tender_fi_discounts_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.tender_fi_discounts ADD CONSTRAINT tender_fi_discounts_created_by_fkey FOREIGN KEY (created_by) REFERENCES auth.users(id);
ALTER TABLE public.tender_fi_discounts ADD CONSTRAINT tender_fi_discounts_rules_is_array CHECK (jsonb_typeof(rules) = 'array');
ALTER TABLE public.tender_fi_discounts ADD CONSTRAINT tender_fi_discounts_mode_check CHECK (mode IN ('discount', 'zeroing'));
ALTER TABLE public.tender_fi_discounts ADD CONSTRAINT tender_fi_discounts_zeroed_is_array CHECK (jsonb_typeof(zeroed_position_ids) = 'array');

-- projects
ALTER TABLE public.projects ADD CONSTRAINT projects_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id);
ALTER TABLE public.projects ADD CONSTRAINT projects_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);
ALTER TABLE public.project_additional_agreements ADD CONSTRAINT project_additional_agreements_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;
ALTER TABLE public.project_monthly_completion ADD CONSTRAINT project_monthly_completion_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;

-- tender_groups / tender_group_members / tender_iterations
ALTER TABLE public.tender_groups ADD CONSTRAINT tender_groups_tender_id_fkey FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.tender_groups ADD CONSTRAINT tender_groups_quality_updated_by_fkey FOREIGN KEY (quality_updated_by) REFERENCES public.users(id);
ALTER TABLE public.tender_group_members ADD CONSTRAINT tender_group_members_group_id_fkey FOREIGN KEY (group_id) REFERENCES public.tender_groups(id) ON DELETE CASCADE;
ALTER TABLE public.tender_group_members ADD CONSTRAINT tender_group_members_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;
ALTER TABLE public.tender_iterations ADD CONSTRAINT tender_iterations_group_id_fkey FOREIGN KEY (group_id) REFERENCES public.tender_groups(id) ON DELETE CASCADE;
ALTER TABLE public.tender_iterations ADD CONSTRAINT tender_iterations_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;
ALTER TABLE public.tender_iterations ADD CONSTRAINT tender_iterations_manager_id_fkey FOREIGN KEY (manager_id) REFERENCES public.users(id);

-- ===========================================================================
-- 5. NON-CONSTRAINT INDEXES (CREATE INDEX IF NOT EXISTS — idempotent)
-- ===========================================================================

-- boq_items
CREATE INDEX IF NOT EXISTS idx_boq_items_boq_item_type ON public.boq_items USING btree (boq_item_type);
CREATE INDEX IF NOT EXISTS idx_boq_items_client_position_id ON public.boq_items USING btree (client_position_id);
CREATE INDEX IF NOT EXISTS idx_boq_items_detail_cost_category_id ON public.boq_items USING btree (detail_cost_category_id);
CREATE INDEX IF NOT EXISTS idx_boq_items_material_name_id ON public.boq_items USING btree (material_name_id);
CREATE INDEX IF NOT EXISTS idx_boq_items_parent_work_item_id ON public.boq_items USING btree (parent_work_item_id);
CREATE INDEX IF NOT EXISTS idx_boq_items_position_sort ON public.boq_items USING btree (client_position_id, sort_number);
CREATE INDEX IF NOT EXISTS idx_boq_items_tender_id ON public.boq_items USING btree (tender_id);
CREATE INDEX IF NOT EXISTS idx_boq_items_work_name_id ON public.boq_items USING btree (work_name_id);

-- client_positions
CREATE INDEX IF NOT EXISTS idx_client_positions_is_additional ON public.client_positions USING btree (tender_id, is_additional);
CREATE INDEX IF NOT EXISTS idx_client_positions_parent_id ON public.client_positions USING btree (parent_position_id);
CREATE INDEX IF NOT EXISTS idx_client_positions_position_number ON public.client_positions USING btree (tender_id, position_number);
CREATE INDEX IF NOT EXISTS idx_client_positions_tender_id ON public.client_positions USING btree (tender_id);

-- construction_cost_volumes
CREATE INDEX IF NOT EXISTS idx_construction_cost_volumes_detail_cost ON public.construction_cost_volumes USING btree (detail_cost_category_id);
CREATE INDEX IF NOT EXISTS idx_construction_cost_volumes_tender ON public.construction_cost_volumes USING btree (tender_id);
-- Партиальные UNIQUE-индексы: один объём на (tender, detail) и на (tender, group_key).
-- Были в Supabase PROD, потеряны при cutover в Yandex — из-за их отсутствия
-- неатомарный upsert порождал дубли строк («объём не сохраняется»). Восстановлены.
CREATE UNIQUE INDEX IF NOT EXISTS construction_cost_volumes_tender_detail_key ON public.construction_cost_volumes USING btree (tender_id, detail_cost_category_id) WHERE (detail_cost_category_id IS NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS construction_cost_volumes_tender_group_key ON public.construction_cost_volumes USING btree (tender_id, group_key) WHERE (group_key IS NOT NULL);

-- cost_categories
CREATE INDEX IF NOT EXISTS idx_cost_categories_created_at ON public.cost_categories USING btree (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cost_categories_name ON public.cost_categories USING btree (name);
CREATE INDEX IF NOT EXISTS idx_cost_categories_unit ON public.cost_categories USING btree (unit);

-- cost_redistribution_results
CREATE INDEX IF NOT EXISTS idx_redistribution_boq_item ON public.cost_redistribution_results USING btree (boq_item_id);
CREATE INDEX IF NOT EXISTS idx_redistribution_tender_tactic ON public.cost_redistribution_results USING btree (tender_id, markup_tactic_id);

-- tender_fi_discounts (tender_id уже покрыт UNIQUE-констрейнтом)
CREATE INDEX IF NOT EXISTS idx_tender_fi_discounts_created_by ON public.tender_fi_discounts USING btree (created_by);

-- detail_cost_categories
CREATE INDEX IF NOT EXISTS idx_detail_cost_categories_category_id ON public.detail_cost_categories USING btree (cost_category_id);
CREATE INDEX IF NOT EXISTS idx_detail_cost_categories_composite ON public.detail_cost_categories USING btree (cost_category_id, location);
CREATE INDEX IF NOT EXISTS idx_detail_cost_categories_location ON public.detail_cost_categories USING btree (location);
CREATE INDEX IF NOT EXISTS idx_detail_cost_categories_name ON public.detail_cost_categories USING btree (name);
CREATE INDEX IF NOT EXISTS idx_detail_cost_categories_order_num ON public.detail_cost_categories USING btree (order_num);
CREATE INDEX IF NOT EXISTS idx_detail_cost_categories_unit ON public.detail_cost_categories USING btree (unit);

-- markup_parameters
CREATE INDEX IF NOT EXISTS idx_markup_parameters_is_active ON public.markup_parameters USING btree (is_active);
CREATE INDEX IF NOT EXISTS idx_markup_parameters_key ON public.markup_parameters USING btree (key);
CREATE INDEX IF NOT EXISTS idx_markup_parameters_order_num ON public.markup_parameters USING btree (order_num);

-- markup_tactics
CREATE INDEX IF NOT EXISTS idx_markup_tactics_created_at ON public.markup_tactics USING btree (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_markup_tactics_is_global ON public.markup_tactics USING btree (is_global);
CREATE INDEX IF NOT EXISTS idx_markup_tactics_user_id ON public.markup_tactics USING btree (user_id);

-- material_names
CREATE INDEX IF NOT EXISTS idx_material_names_name ON public.material_names USING btree (name);
CREATE INDEX IF NOT EXISTS idx_material_names_unit ON public.material_names USING btree (unit);

-- materials_library
CREATE INDEX IF NOT EXISTS idx_materials_library_created_at ON public.materials_library USING btree (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_materials_library_currency_type ON public.materials_library USING btree (currency_type);
CREATE INDEX IF NOT EXISTS idx_materials_library_delivery_price_type ON public.materials_library USING btree (delivery_price_type);
CREATE INDEX IF NOT EXISTS idx_materials_library_item_type ON public.materials_library USING btree (item_type);
CREATE INDEX IF NOT EXISTS idx_materials_library_material_name_id ON public.materials_library USING btree (material_name_id);
CREATE INDEX IF NOT EXISTS idx_materials_library_material_type ON public.materials_library USING btree (material_type);
CREATE INDEX IF NOT EXISTS idx_materials_library_type_currency ON public.materials_library USING btree (material_type, currency_type);

-- template_items
CREATE INDEX IF NOT EXISTS idx_template_items_detail_cost_category_id ON public.template_items USING btree (detail_cost_category_id);
CREATE INDEX IF NOT EXISTS idx_template_items_kind ON public.template_items USING btree (kind);
CREATE INDEX IF NOT EXISTS idx_template_items_material_library_id ON public.template_items USING btree (material_library_id);
CREATE INDEX IF NOT EXISTS idx_template_items_parent_work_item_id ON public.template_items USING btree (parent_work_item_id);
CREATE INDEX IF NOT EXISTS idx_template_items_template_id ON public.template_items USING btree (template_id);
CREATE INDEX IF NOT EXISTS idx_template_items_work_library_id ON public.template_items USING btree (work_library_id);

-- templates
CREATE INDEX IF NOT EXISTS idx_templates_detail_cost_category_id ON public.templates USING btree (detail_cost_category_id);

-- tender_documents
CREATE INDEX IF NOT EXISTS idx_tender_documents_content_fts ON public.tender_documents USING gin (to_tsvector('russian'::regconfig, content_markdown));
CREATE INDEX IF NOT EXISTS idx_tender_documents_section ON public.tender_documents USING btree (section_type);
CREATE INDEX IF NOT EXISTS idx_tender_documents_tender ON public.tender_documents USING btree (tender_id);
CREATE INDEX IF NOT EXISTS idx_tender_documents_uploaded ON public.tender_documents USING btree (upload_date DESC);

-- tender_markup_percentage
CREATE INDEX IF NOT EXISTS idx_tender_markup_percentage_markup_parameter_id ON public.tender_markup_percentage USING btree (markup_parameter_id);
CREATE INDEX IF NOT EXISTS idx_tender_markup_percentage_tender_id ON public.tender_markup_percentage USING btree (tender_id);

-- tender_pricing_distribution
CREATE INDEX IF NOT EXISTS idx_tender_pricing_distribution_tactic_id ON public.tender_pricing_distribution USING btree (markup_tactic_id);
CREATE INDEX IF NOT EXISTS idx_tender_pricing_distribution_tender_id ON public.tender_pricing_distribution USING btree (tender_id);

-- tenders
CREATE INDEX IF NOT EXISTS idx_tenders_client_name ON public.tenders USING btree (client_name);
CREATE INDEX IF NOT EXISTS idx_tenders_created_at ON public.tenders USING btree (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tenders_submission_deadline ON public.tenders USING btree (submission_deadline);
CREATE INDEX IF NOT EXISTS idx_tenders_tender_number ON public.tenders USING btree (tender_number);

-- units
CREATE INDEX IF NOT EXISTS idx_units_category ON public.units USING btree (category);
CREATE INDEX IF NOT EXISTS idx_units_is_active ON public.units USING btree (is_active);
CREATE INDEX IF NOT EXISTS idx_units_sort_order ON public.units USING btree (sort_order);

-- user_tasks
CREATE INDEX IF NOT EXISTS idx_user_tasks_status ON public.user_tasks USING btree (task_status);
CREATE INDEX IF NOT EXISTS idx_user_tasks_tender_id ON public.user_tasks USING btree (tender_id);
CREATE INDEX IF NOT EXISTS idx_user_tasks_user_id ON public.user_tasks USING btree (user_id);

-- users
CREATE INDEX IF NOT EXISTS idx_users_access_status ON public.users USING btree (access_status);
CREATE INDEX IF NOT EXISTS idx_users_approved_by ON public.users USING btree (approved_by);
CREATE INDEX IF NOT EXISTS idx_users_deadline_extensions ON public.users USING gin (tender_deadline_extensions);
CREATE INDEX IF NOT EXISTS idx_users_email ON public.users USING btree (email);
CREATE INDEX IF NOT EXISTS idx_users_role_code ON public.users USING btree (role_code);

-- work_names
CREATE INDEX IF NOT EXISTS idx_work_names_name ON public.work_names USING btree (name);
CREATE INDEX IF NOT EXISTS idx_work_names_unit ON public.work_names USING btree (unit);

-- works_library
CREATE INDEX IF NOT EXISTS idx_works_library_created_at ON public.works_library USING btree (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_works_library_currency_type ON public.works_library USING btree (currency_type);
CREATE INDEX IF NOT EXISTS idx_works_library_item_type ON public.works_library USING btree (item_type);
CREATE INDEX IF NOT EXISTS idx_works_library_type_currency ON public.works_library USING btree (item_type, currency_type);
CREATE INDEX IF NOT EXISTS idx_works_library_work_name_id ON public.works_library USING btree (work_name_id);

-- FK-gap indexes (Supabase Performance Advisor: unindexed_foreign_keys)
CREATE INDEX IF NOT EXISTS idx_boq_items_unit_code ON public.boq_items(unit_code);
CREATE INDEX IF NOT EXISTS idx_client_positions_unit_code ON public.client_positions(unit_code);
CREATE INDEX IF NOT EXISTS idx_comparison_notes_tender_id_2 ON public.comparison_notes(tender_id_2);
CREATE INDEX IF NOT EXISTS idx_comparison_notes_created_by ON public.comparison_notes(created_by);
CREATE INDEX IF NOT EXISTS idx_cost_redistribution_created_by ON public.cost_redistribution_results(created_by);
CREATE INDEX IF NOT EXISTS idx_import_sessions_cancelled_by ON public.import_sessions(cancelled_by);
CREATE INDEX IF NOT EXISTS idx_subcontract_growth_detail_cost ON public.subcontract_growth_exclusions(detail_cost_category_id);
CREATE INDEX IF NOT EXISTS idx_tender_group_members_user_id ON public.tender_group_members(user_id);
CREATE INDEX IF NOT EXISTS idx_tender_groups_quality_updated_by ON public.tender_groups(quality_updated_by);
CREATE INDEX IF NOT EXISTS idx_tender_iterations_manager_id ON public.tender_iterations(manager_id);
CREATE INDEX IF NOT EXISTS idx_tender_iterations_user_id ON public.tender_iterations(user_id);
CREATE INDEX IF NOT EXISTS idx_tender_registry_created_by ON public.tender_registry(created_by);
CREATE INDEX IF NOT EXISTS idx_tenders_created_by ON public.tenders(created_by);
CREATE INDEX IF NOT EXISTS idx_tenders_markup_tactic_id ON public.tenders(markup_tactic_id);
CREATE INDEX IF NOT EXISTS idx_user_position_filters_tender_id ON public.user_position_filters(tender_id);
CREATE INDEX IF NOT EXISTS idx_boq_items_audit_item_date ON public.boq_items_audit(boq_item_id, changed_at DESC);
-- Audit-history read path (ListByPosition) filters by the audited row's
-- client_position_id pulled from the JSONB snapshot. Without these expression
-- indexes the OR'd predicate forces a full seqscan of boq_items_audit.
-- (Present in the original Supabase schema; carried over to Yandex here.)
CREATE INDEX IF NOT EXISTS idx_audit_new_position ON public.boq_items_audit ((new_data ->> 'client_position_id'));
CREATE INDEX IF NOT EXISTS idx_audit_old_position ON public.boq_items_audit ((old_data ->> 'client_position_id'));

-- Remaining FK-gap indexes (PROD migration 10).
CREATE INDEX IF NOT EXISTS idx_boq_items_import_session_id ON public.boq_items(import_session_id);
CREATE INDEX IF NOT EXISTS idx_boq_items_audit_changed_by ON public.boq_items_audit(changed_by);
CREATE INDEX IF NOT EXISTS idx_cost_redistribution_markup_tactic ON public.cost_redistribution_results(markup_tactic_id);
CREATE INDEX IF NOT EXISTS idx_import_sessions_tender_id ON public.import_sessions(tender_id);
CREATE INDEX IF NOT EXISTS idx_import_sessions_user_id ON public.import_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_library_folders_parent_id ON public.library_folders(parent_id);
CREATE INDEX IF NOT EXISTS idx_materials_library_folder_id ON public.materials_library(folder_id);
CREATE INDEX IF NOT EXISTS idx_project_additional_agreements_project_id ON public.project_additional_agreements(project_id);
CREATE INDEX IF NOT EXISTS idx_projects_tender_id ON public.projects(tender_id);
CREATE INDEX IF NOT EXISTS idx_templates_folder_id ON public.templates(folder_id);
CREATE INDEX IF NOT EXISTS idx_tender_notes_user_id ON public.tender_notes(user_id);
CREATE INDEX IF NOT EXISTS idx_tender_registry_construction_scope_id ON public.tender_registry(construction_scope_id);
CREATE INDEX IF NOT EXISTS idx_tender_registry_status_id ON public.tender_registry(status_id);
CREATE INDEX IF NOT EXISTS idx_user_position_filters_position_id ON public.user_position_filters(position_id);
CREATE INDEX IF NOT EXISTS idx_works_library_folder_id ON public.works_library(folder_id);

-- Этап 2.3: Smart Import Memory
ALTER TABLE public.boq_import_mapping_profiles ADD CONSTRAINT boq_import_mapping_profiles_pkey PRIMARY KEY (id);
ALTER TABLE public.boq_import_mapping_profiles ADD CONSTRAINT boq_import_mapping_profiles_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_boq_import_mapping_profiles_user_sig
    ON public.boq_import_mapping_profiles (user_id, normalized_header_signature) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_boq_import_mapping_profiles_user
    ON public.boq_import_mapping_profiles (user_id, updated_at DESC);

ALTER TABLE public.nomenclature_import_aliases ADD CONSTRAINT nomenclature_import_aliases_pkey PRIMARY KEY (id);
ALTER TABLE public.nomenclature_import_aliases ADD CONSTRAINT nomenclature_import_aliases_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;
-- hard delete каталога не блокируем: alias уходит каскадом (без dangling ID).
ALTER TABLE public.nomenclature_import_aliases ADD CONSTRAINT nomenclature_import_aliases_material_name_id_fkey
    FOREIGN KEY (material_name_id) REFERENCES public.material_names(id) ON DELETE CASCADE;
ALTER TABLE public.nomenclature_import_aliases ADD CONSTRAINT nomenclature_import_aliases_work_name_id_fkey
    FOREIGN KEY (work_name_id) REFERENCES public.work_names(id) ON DELETE CASCADE;
ALTER TABLE public.nomenclature_import_aliases ADD CONSTRAINT nomenclature_import_aliases_detail_cost_category_id_fkey
    FOREIGN KEY (detail_cost_category_id) REFERENCES public.detail_cost_categories(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX IF NOT EXISTS uq_nomenclature_import_aliases_active_key
    ON public.nomenclature_import_aliases (
        user_id, catalog_kind, normalized_source_text, canonical_boq_item_type,
        COALESCE(normalized_unit_code, ''),
        COALESCE(detail_cost_category_id, '00000000-0000-0000-0000-000000000000'::uuid)
    ) WHERE is_active;
CREATE INDEX IF NOT EXISTS idx_nomenclature_import_aliases_user
    ON public.nomenclature_import_aliases (user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_nomenclature_import_aliases_material
    ON public.nomenclature_import_aliases (material_name_id) WHERE material_name_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_nomenclature_import_aliases_work
    ON public.nomenclature_import_aliases (work_name_id) WHERE work_name_id IS NOT NULL;

-- Этап 2.4 (§8): структурная целостность BOQ-связей (см. incremental
-- 2026_07_boq_relation_integrity.sql; в baseline данные заведомо чистые).
ALTER TABLE public.client_positions ADD CONSTRAINT client_positions_id_tender_uniq UNIQUE (id, tender_id);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_id_scope_uniq UNIQUE (id, tender_id, client_position_id);
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_position_scope_fkey
    FOREIGN KEY (client_position_id, tender_id)
    REFERENCES public.client_positions (id, tender_id) ON DELETE CASCADE;
ALTER TABLE public.boq_items ADD CONSTRAINT boq_items_parent_scope_fkey
    FOREIGN KEY (parent_work_item_id, tender_id, client_position_id)
    REFERENCES public.boq_items (id, tender_id, client_position_id) ON DELETE CASCADE;

-- Этап 2.5: OpenRouter AI Administration
ALTER TABLE public.ai_feature_settings ADD CONSTRAINT ai_feature_settings_pkey PRIMARY KEY (feature_code);
ALTER TABLE public.ai_feature_settings ADD CONSTRAINT ai_feature_settings_updated_by_fkey
    FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE SET NULL;

-- Этап 2.6: Controlled AI Rollout
ALTER TABLE public.ai_pilot_users ADD CONSTRAINT ai_pilot_users_pkey PRIMARY KEY (feature_code, user_id);
ALTER TABLE public.ai_pilot_users ADD CONSTRAINT ai_pilot_users_feature_code_fkey
    FOREIGN KEY (feature_code) REFERENCES public.ai_feature_settings(feature_code) ON DELETE CASCADE;
ALTER TABLE public.ai_pilot_users ADD CONSTRAINT ai_pilot_users_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;
ALTER TABLE public.ai_pilot_users ADD CONSTRAINT ai_pilot_users_added_by_fkey
    FOREIGN KEY (added_by) REFERENCES public.users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_ai_pilot_users_active
    ON public.ai_pilot_users (feature_code, is_active) WHERE is_active;

ALTER TABLE public.ai_usage_requests ADD CONSTRAINT ai_usage_requests_pkey PRIMARY KEY (id);
ALTER TABLE public.ai_usage_requests ADD CONSTRAINT ai_usage_requests_feature_code_fkey
    FOREIGN KEY (feature_code) REFERENCES public.ai_feature_settings(feature_code) ON DELETE CASCADE;
ALTER TABLE public.ai_usage_requests ADD CONSTRAINT ai_usage_requests_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_ai_usage_requests_user_day
    ON public.ai_usage_requests (feature_code, user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ai_usage_requests_month
    ON public.ai_usage_requests (feature_code, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ai_usage_requests_reserved
    ON public.ai_usage_requests (reservation_expires_at) WHERE request_status = 'reserved';

ALTER TABLE public.ai_row_feedback ADD CONSTRAINT ai_row_feedback_pkey PRIMARY KEY (id);
ALTER TABLE public.ai_row_feedback ADD CONSTRAINT ai_row_feedback_request_id_fkey
    FOREIGN KEY (request_id) REFERENCES public.ai_usage_requests(id) ON DELETE CASCADE;
ALTER TABLE public.ai_row_feedback ADD CONSTRAINT ai_row_feedback_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;
ALTER TABLE public.ai_row_feedback ADD CONSTRAINT ai_row_feedback_request_row_uniq UNIQUE (request_id, row_context_hash);
CREATE INDEX IF NOT EXISTS idx_ai_row_feedback_request ON public.ai_row_feedback (request_id);
CREATE INDEX IF NOT EXISTS idx_ai_row_feedback_user ON public.ai_row_feedback (user_id, created_at DESC);

ALTER TABLE public.ai_circuit_state ADD CONSTRAINT ai_circuit_state_pkey PRIMARY KEY (feature_code);
ALTER TABLE public.ai_circuit_state ADD CONSTRAINT ai_circuit_state_feature_code_fkey
    FOREIGN KEY (feature_code) REFERENCES public.ai_feature_settings(feature_code) ON DELETE CASCADE;

ALTER TABLE public.ai_evaluation_summaries ADD CONSTRAINT ai_evaluation_summaries_pkey PRIMARY KEY (id);
ALTER TABLE public.ai_evaluation_summaries ADD CONSTRAINT ai_evaluation_summaries_feature_code_fkey
    FOREIGN KEY (feature_code) REFERENCES public.ai_feature_settings(feature_code) ON DELETE CASCADE;
ALTER TABLE public.ai_evaluation_summaries ADD CONSTRAINT ai_evaluation_summaries_executed_by_fkey
    FOREIGN KEY (executed_by) REFERENCES public.users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_ai_evaluation_summaries_feature
    ON public.ai_evaluation_summaries (feature_code, executed_at DESC);

ALTER TABLE public.ai_feature_settings ADD CONSTRAINT ai_feature_settings_last_eval_fkey
    FOREIGN KEY (last_live_evaluation_id) REFERENCES public.ai_evaluation_summaries(id) ON DELETE SET NULL;
-- ----- quality_acknowledgements ---------------------------------------------
-- Вердикты инженера по находкам правил проверки данных.
-- Уникальность по (tender_id, rule_code, entity_id): один действующий вердикт
-- на сущность в рамках правила, перезаписывается при смене решения.
ALTER TABLE public.quality_acknowledgements
    ADD CONSTRAINT quality_acknowledgements_pkey PRIMARY KEY (id);
ALTER TABLE public.quality_acknowledgements
    ADD CONSTRAINT quality_acknowledgements_verdict_check
    CHECK (verdict IN ('accepted', 'error'));
ALTER TABLE public.quality_acknowledgements
    ADD CONSTRAINT quality_acknowledgements_tender_fkey
    FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;

CREATE UNIQUE INDEX IF NOT EXISTS quality_acknowledgements_unique_idx
    ON public.quality_acknowledgements (tender_id, rule_code, entity_id);
CREATE INDEX IF NOT EXISTS quality_acknowledgements_tender_idx
    ON public.quality_acknowledgements (tender_id);

-- ----- verification_runs / verification_findings / events -------------------
-- Прогоны каталога правил и материализованное состояние находок. «Новое» —
-- то, что появилось после отметки «Проверка завершена» (trigger_source='checkpoint').
ALTER TABLE public.verification_runs
    ADD CONSTRAINT verification_runs_pkey PRIMARY KEY (id);
ALTER TABLE public.verification_runs
    ADD CONSTRAINT verification_runs_trigger_check
    CHECK (trigger_source IN ('checkpoint', 'view', 'api', 'auto'));
ALTER TABLE public.verification_runs
    ADD CONSTRAINT verification_runs_tender_fkey
    FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS verification_runs_tender_started_idx
    ON public.verification_runs (tender_id, started_at DESC);
CREATE INDEX IF NOT EXISTS verification_runs_checkpoint_idx
    ON public.verification_runs (tender_id, started_at DESC)
    WHERE trigger_source = 'checkpoint';

ALTER TABLE public.verification_findings
    ADD CONSTRAINT verification_findings_pkey PRIMARY KEY (id);
ALTER TABLE public.verification_findings
    ADD CONSTRAINT verification_findings_source_check
    CHECK (source IN ('rules'));
ALTER TABLE public.verification_findings
    ADD CONSTRAINT verification_findings_entity_type_check
    CHECK (entity_type IN ('boq_item', 'client_position', 'material_name', 'tender'));
ALTER TABLE public.verification_findings
    ADD CONSTRAINT verification_findings_severity_check
    CHECK (severity IN ('error', 'warning', 'info'));
ALTER TABLE public.verification_findings
    ADD CONSTRAINT verification_findings_tender_fkey
    FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.verification_findings
    ADD CONSTRAINT verification_findings_first_run_fkey
    FOREIGN KEY (first_seen_run_id) REFERENCES public.verification_runs(id) ON DELETE SET NULL;
ALTER TABLE public.verification_findings
    ADD CONSTRAINT verification_findings_last_run_fkey
    FOREIGN KEY (last_seen_run_id) REFERENCES public.verification_runs(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS verification_findings_unique_idx
    ON public.verification_findings (tender_id, source, rule_code, entity_type, entity_id);
CREATE INDEX IF NOT EXISTS verification_findings_open_idx
    ON public.verification_findings (tender_id, rule_code)
    WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS verification_findings_position_idx
    ON public.verification_findings (client_position_id)
    WHERE client_position_id IS NOT NULL;

ALTER TABLE public.verification_finding_events
    ADD CONSTRAINT verification_finding_events_pkey PRIMARY KEY (id);
ALTER TABLE public.verification_finding_events
    ADD CONSTRAINT verification_finding_events_type_check
    CHECK (event_type IN ('opened', 'reopened', 'resolved', 'accepted', 'error'));
ALTER TABLE public.verification_finding_events
    ADD CONSTRAINT verification_finding_events_source_check
    CHECK (source IN ('system', 'ui', 'telegram', 'api'));
ALTER TABLE public.verification_finding_events
    ADD CONSTRAINT verification_finding_events_finding_fkey
    FOREIGN KEY (finding_id) REFERENCES public.verification_findings(id) ON DELETE CASCADE;
ALTER TABLE public.verification_finding_events
    ADD CONSTRAINT verification_finding_events_run_fkey
    FOREIGN KEY (run_id) REFERENCES public.verification_runs(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS verification_finding_events_finding_idx
    ON public.verification_finding_events (finding_id, created_at DESC);

-- ----- verification_section_states / events ---------------------------------
-- Отметки «раздел расценён» / «раздел проверен» с хешем содержимого раздела.
ALTER TABLE public.verification_section_states
    ADD CONSTRAINT verification_section_states_pkey PRIMARY KEY (id);
ALTER TABLE public.verification_section_states
    ADD CONSTRAINT verification_section_states_stage_check
    CHECK (stage IN ('review'));
ALTER TABLE public.verification_section_states
    ADD CONSTRAINT verification_section_states_tender_fkey
    FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX IF NOT EXISTS verification_section_states_unique_idx
    ON public.verification_section_states (tender_id, section_key, stage);

ALTER TABLE public.verification_section_events
    ADD CONSTRAINT verification_section_events_pkey PRIMARY KEY (id);
ALTER TABLE public.verification_section_events
    ADD CONSTRAINT verification_section_events_stage_check
    CHECK (stage IN ('review'));
ALTER TABLE public.verification_section_events
    ADD CONSTRAINT verification_section_events_action_check
    CHECK (action IN ('marked', 'unmarked'));
ALTER TABLE public.verification_section_events
    ADD CONSTRAINT verification_section_events_tender_fkey
    FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS verification_section_events_tender_idx
    ON public.verification_section_events (tender_id, section_key, created_at DESC);

-- ----- benchmark_ranges -----------------------------------------------------
-- Ручные эталонные диапазоны; при сравнении важнее исторической статистики.
ALTER TABLE public.benchmark_ranges
    ADD CONSTRAINT benchmark_ranges_pkey PRIMARY KEY (id);
ALTER TABLE public.benchmark_ranges
    ADD CONSTRAINT benchmark_ranges_metric_check
    CHECK (metric_kind IN ('per_volume_unit', 'per_area_sp'));
ALTER TABLE public.benchmark_ranges
    ADD CONSTRAINT benchmark_ranges_level_check
    CHECK (level IN ('total', 'category', 'detail'));
ALTER TABLE public.benchmark_ranges
    ADD CONSTRAINT benchmark_ranges_target_check CHECK (
        (level = 'total' AND cost_category_id IS NULL AND detail_cost_category_id IS NULL
            AND metric_kind = 'per_area_sp')
        OR (level = 'category' AND cost_category_id IS NOT NULL AND detail_cost_category_id IS NULL)
        OR (level = 'detail' AND detail_cost_category_id IS NOT NULL AND cost_category_id IS NULL));
ALTER TABLE public.benchmark_ranges
    ADD CONSTRAINT benchmark_ranges_bounds_check CHECK (
        (min_value IS NOT NULL OR max_value IS NOT NULL)
        AND (min_value IS NULL OR min_value >= 0)
        AND (max_value IS NULL OR max_value >= 0)
        AND (min_value IS NULL OR max_value IS NULL OR min_value <= max_value));
ALTER TABLE public.benchmark_ranges
    ADD CONSTRAINT benchmark_ranges_category_fkey
    FOREIGN KEY (cost_category_id) REFERENCES public.cost_categories(id) ON DELETE CASCADE;
ALTER TABLE public.benchmark_ranges
    ADD CONSTRAINT benchmark_ranges_detail_fkey
    FOREIGN KEY (detail_cost_category_id) REFERENCES public.detail_cost_categories(id) ON DELETE CASCADE;
-- NULLS NOT DISTINCT (PostgreSQL 15+): NULL в классе или объёме означает «любой»,
-- и два диапазона «для любого класса» на одну цель — дубль. Выражения вида
-- housing_class::text в индексе недопустимы (приведение enum не IMMUTABLE).
CREATE UNIQUE INDEX IF NOT EXISTS benchmark_ranges_active_unique_idx
    ON public.benchmark_ranges (
        metric_kind, level, cost_category_id, detail_cost_category_id,
        housing_class, construction_scope)
    NULLS NOT DISTINCT
    WHERE is_active;

-- ----- tender_briefs --------------------------------------------------------
ALTER TABLE public.tender_briefs
    ADD CONSTRAINT tender_briefs_pkey PRIMARY KEY (tender_id);
ALTER TABLE public.tender_briefs
    ADD CONSTRAINT tender_briefs_tender_fkey
    FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;

-- ----- telegram / verification_notifications -------------------------------
ALTER TABLE public.telegram_links ADD CONSTRAINT telegram_links_pkey PRIMARY KEY (user_id);
ALTER TABLE public.telegram_links ADD CONSTRAINT telegram_links_chat_key UNIQUE (chat_id);
ALTER TABLE public.telegram_links ADD CONSTRAINT telegram_links_user_fkey
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE public.telegram_link_tokens ADD CONSTRAINT telegram_link_tokens_pkey PRIMARY KEY (token_hash);
ALTER TABLE public.telegram_link_tokens ADD CONSTRAINT telegram_link_tokens_user_fkey
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS telegram_link_tokens_user_idx ON public.telegram_link_tokens (user_id);

ALTER TABLE public.telegram_bot_state ADD CONSTRAINT telegram_bot_state_pkey PRIMARY KEY (id);
ALTER TABLE public.telegram_bot_state ADD CONSTRAINT telegram_bot_state_single_check CHECK (id = 1);

ALTER TABLE public.verification_notifications ADD CONSTRAINT verification_notifications_pkey PRIMARY KEY (id);
ALTER TABLE public.verification_notifications ADD CONSTRAINT verification_notifications_status_check
    CHECK (status IN ('pending', 'sent', 'failed', 'skipped'));
ALTER TABLE public.verification_notifications ADD CONSTRAINT verification_notifications_tender_fkey
    FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE CASCADE;
ALTER TABLE public.verification_notifications ADD CONSTRAINT verification_notifications_recipient_fkey
    FOREIGN KEY (recipient_user_id) REFERENCES public.users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS verification_notifications_pending_idx
    ON public.verification_notifications (next_attempt_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS verification_notifications_tender_idx
    ON public.verification_notifications (tender_id, created_at DESC);

ALTER TABLE public.verification_notification_items ADD CONSTRAINT verification_notification_items_pkey PRIMARY KEY (id);
ALTER TABLE public.verification_notification_items ADD CONSTRAINT verification_notification_items_resolver_check
    CHECK (resolver IN ('item_author', 'position_author', 'sender'));
ALTER TABLE public.verification_notification_items ADD CONSTRAINT verification_notification_items_verdict_check
    CHECK (verdict IS NULL OR verdict IN ('accepted', 'error'));
ALTER TABLE public.verification_notification_items ADD CONSTRAINT verification_notification_items_notification_fkey
    FOREIGN KEY (notification_id) REFERENCES public.verification_notifications(id) ON DELETE CASCADE;
ALTER TABLE public.verification_notification_items ADD CONSTRAINT verification_notification_items_finding_fkey
    FOREIGN KEY (finding_id) REFERENCES public.verification_findings(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX IF NOT EXISTS verification_notification_items_dedup_idx
    ON public.verification_notification_items (dedup_key);
CREATE INDEX IF NOT EXISTS verification_notification_items_notification_idx
    ON public.verification_notification_items (notification_id);
CREATE INDEX IF NOT EXISTS verification_notification_items_finding_idx
    ON public.verification_notification_items (finding_id);

-- ----- журнал напоминаний «Перечня тендеров» (API для внешнего бота) -------
ALTER TABLE public.telegram_tender_reminders ADD CONSTRAINT telegram_tender_reminders_pkey
    PRIMARY KEY (chat_id, registry_id, kind, due_on);
ALTER TABLE public.telegram_tender_reminders ADD CONSTRAINT telegram_tender_reminders_kind_check
    CHECK (kind IN ('call', 'waiting_pd'));
ALTER TABLE public.telegram_tender_reminders ADD CONSTRAINT telegram_tender_reminders_status_check
    CHECK (status IN ('pending', 'sent', 'failed', 'skipped'));
ALTER TABLE public.telegram_tender_reminders ADD CONSTRAINT telegram_tender_reminders_registry_fkey
    FOREIGN KEY (registry_id) REFERENCES public.tender_registry(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS telegram_tender_reminders_pending_idx
    ON public.telegram_tender_reminders (created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS telegram_tender_reminders_registry_idx
    ON public.telegram_tender_reminders (registry_id);

-- ─── Машинный доступ к API ──────────────────────────────────────────────────
ALTER TABLE public.api_keys
    ADD CONSTRAINT api_keys_pkey PRIMARY KEY (id);
ALTER TABLE public.api_keys
    ADD CONSTRAINT api_keys_key_hash_uniq UNIQUE (key_hash);
ALTER TABLE public.api_keys
    ADD CONSTRAINT api_keys_name_chk CHECK (length(btrim(name)) BETWEEN 1 AND 120);
-- Ключ без прав только создаёт иллюзию доступа.
ALTER TABLE public.api_keys
    ADD CONSTRAINT api_keys_scopes_chk
    CHECK (cardinality(scopes) > 0
           AND scopes <@ ARRAY['archive:read', 'archive:write', 'tenders:read', 'tenders:write',
                               'verification:read', 'verification:write',
                               'registry:read', 'registry:write']::text[]);
ALTER TABLE public.api_keys
    ADD CONSTRAINT api_keys_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);
ALTER TABLE public.api_keys
    ADD CONSTRAINT api_keys_revoked_by_fkey FOREIGN KEY (revoked_by) REFERENCES public.users(id);
CREATE INDEX IF NOT EXISTS idx_api_keys_created_at ON public.api_keys (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_keys_created_by ON public.api_keys (created_by);

ALTER TABLE public.api_access_settings
    ADD CONSTRAINT api_access_settings_pkey PRIMARY KEY (id);
ALTER TABLE public.api_access_settings
    ADD CONSTRAINT api_access_settings_singleton_chk CHECK (id);
ALTER TABLE public.api_access_settings
    ADD CONSTRAINT api_access_settings_limits_chk
    CHECK (max_search_limit BETWEEN 1 AND 1000
       AND max_candidate_limit BETWEEN 50 AND 20000
       AND max_suggest_queries BETWEEN 1 AND 500
       AND rate_limit_per_minute BETWEEN 0 AND 100000
       AND call_log_retention_days BETWEEN 1 AND 365);
ALTER TABLE public.api_access_settings
    ADD CONSTRAINT api_access_settings_updated_by_fkey
    FOREIGN KEY (updated_by) REFERENCES public.users(id);

ALTER TABLE public.api_call_log
    ADD CONSTRAINT api_call_log_pkey PRIMARY KEY (id);
ALTER TABLE public.api_call_log
    ADD CONSTRAINT api_call_log_api_key_fkey
    FOREIGN KEY (api_key_id) REFERENCES public.api_keys(id) ON DELETE SET NULL;
ALTER TABLE public.api_call_log
    ADD CONSTRAINT api_call_log_user_fkey
    FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_api_call_log_called_at ON public.api_call_log (called_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_call_log_api_key ON public.api_call_log (api_key_id, called_at DESC);

-- ----- verification_ai_* ------------------------------------------------------
ALTER TABLE public.verification_ai_settings ADD CONSTRAINT verification_ai_settings_pkey PRIMARY KEY (id);
ALTER TABLE public.verification_ai_settings ADD CONSTRAINT verification_ai_settings_single_check CHECK (id = 1);
ALTER TABLE public.verification_ai_settings ADD CONSTRAINT verification_ai_settings_limits_check CHECK (
    max_findings_per_run BETWEEN 1 AND 2000
    AND batch_size BETWEEN 1 AND 20
    AND max_output_tokens BETWEEN 256 AND 32000
    AND request_timeout_seconds BETWEEN 10 AND 600
    AND monthly_token_budget > 0
    AND daily_request_limit BETWEEN 1 AND 100000);
ALTER TABLE public.verification_ai_settings ADD CONSTRAINT verification_ai_settings_test_status_check
    CHECK (last_test_status IS NULL OR last_test_status IN ('passed', 'failed'));
ALTER TABLE public.verification_ai_settings ADD CONSTRAINT verification_ai_settings_enabled_check
    CHECK (NOT enabled OR (model_id IS NOT NULL
                           AND last_test_status IS NOT DISTINCT FROM 'passed'
                           AND last_test_model_id IS NOT DISTINCT FROM model_id));

ALTER TABLE public.verification_ai_requests ADD CONSTRAINT verification_ai_requests_pkey PRIMARY KEY (id);
ALTER TABLE public.verification_ai_requests ADD CONSTRAINT verification_ai_requests_trigger_check
    CHECK (trigger_source IN ('auto', 'manual', 'test'));
ALTER TABLE public.verification_ai_requests ADD CONSTRAINT verification_ai_requests_status_check
    CHECK (status IN ('completed', 'failed', 'invalid'));
ALTER TABLE public.verification_ai_requests ADD CONSTRAINT verification_ai_requests_tender_fkey
    FOREIGN KEY (tender_id) REFERENCES public.tenders(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS verification_ai_requests_created_idx
    ON public.verification_ai_requests (created_at DESC);
CREATE INDEX IF NOT EXISTS verification_ai_requests_tender_idx
    ON public.verification_ai_requests (tender_id, created_at DESC);

ALTER TABLE public.verification_ai_assessments ADD CONSTRAINT verification_ai_assessments_pkey PRIMARY KEY (finding_id);
ALTER TABLE public.verification_ai_assessments ADD CONSTRAINT verification_ai_assessments_label_check
    CHECK (label IN ('likely_error', 'likely_ok', 'unsure'));
ALTER TABLE public.verification_ai_assessments ADD CONSTRAINT verification_ai_assessments_finding_fkey
    FOREIGN KEY (finding_id) REFERENCES public.verification_findings(id) ON DELETE CASCADE;
ALTER TABLE public.verification_ai_assessments ADD CONSTRAINT verification_ai_assessments_request_fkey
    FOREIGN KEY (request_id) REFERENCES public.verification_ai_requests(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS verification_ai_assessments_tender_idx
    ON public.verification_ai_assessments (tender_id);
