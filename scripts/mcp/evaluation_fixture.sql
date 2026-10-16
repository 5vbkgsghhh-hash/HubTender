-- Disposable staging/evaluation database only. Never apply to production.
\set ON_ERROR_STOP on
BEGIN;

INSERT INTO public.units(code,name) VALUES ('шт','Штука') ON CONFLICT (code) DO NOTHING;
INSERT INTO public.cost_categories(id,name,unit) VALUES ('eeeeeeee-0000-0000-0000-000000000001','Оборудование','шт') ON CONFLICT (id) DO NOTHING;
INSERT INTO public.detail_cost_categories(id,cost_category_id,location,name,unit)
VALUES ('eeeeeeee-0000-0000-0000-000000000002','eeeeeeee-0000-0000-0000-000000000001','Объект','Технологическое оборудование','шт') ON CONFLICT (id) DO NOTHING;

INSERT INTO public.material_names(id,name,unit) VALUES
 ('eeeeeeee-1000-0000-0000-000000000001','МОБИЛЬНЫЕ КОМПАКТОРЫ PRESSMAX СЕРИИ 900','шт'),
 ('eeeeeeee-1000-0000-0000-000000000002','Знак ГБО','шт'),
 ('eeeeeeee-1000-0000-0000-000000000003','Автоматизированное рабочее место охраны','шт'),
 ('eeeeeeee-1000-0000-0000-000000000004','Насосная установка','шт'),
 ('eeeeeeee-1000-0000-0000-000000000005','Колесоотбойник металлический','шт')
ON CONFLICT (id) DO NOTHING;
INSERT INTO public.work_names(id,name,unit) VALUES
 ('eeeeeeee-2000-0000-0000-000000000001','Монтаж технологического оборудования','шт'),
 ('eeeeeeee-2000-0000-0000-000000000002','Монтаж мебели','шт'),
 ('eeeeeeee-2000-0000-0000-000000000003','Установка колесоотбойника','шт')
ON CONFLICT (id) DO NOTHING;

INSERT INTO public.tenders(id,title,client_name,tender_number,version,is_archived,usd_rate,eur_rate,created_at,updated_at) VALUES
 ('eeeeeeee-3000-0000-0000-000000000001','MCP EVAL — Целевой тендер','Eval','EVAL-NEW',1,false,90,100,'2026-09-01','2026-09-01'),
 ('eeeeeeee-3000-0000-0000-000000000002','ЖК Сокольники','Eval','EVAL-298',5,true,76.25,89.14,'2026-05-03','2026-05-03'),
 ('eeeeeeee-3000-0000-0000-000000000003','ЖК Символ 4А','Eval','EVAL-311',5,true,84,97.29,'2026-05-12','2026-05-12'),
 ('eeeeeeee-3000-0000-0000-000000000004','MCP EVAL — Архив охраны','Eval','EVAL-SEC',2,true,80,95,'2026-02-01','2026-02-01'),
 ('eeeeeeee-3000-0000-0000-000000000005','MCP EVAL — Старый насос','Eval','EVAL-OLD',1,true,70,90,'2025-01-10','2025-01-10')
ON CONFLICT (id) DO NOTHING;

INSERT INTO public.client_positions(id,tender_id,position_number,unit_code,volume,work_name,hierarchy_level) VALUES
 ('eeeeeeee-4000-0000-0000-000000000001','eeeeeeee-3000-0000-0000-000000000001',1,'шт',2,'Мобильный пресс-компактор',0),
 ('eeeeeeee-4000-0000-0000-000000000002','eeeeeeee-3000-0000-0000-000000000002',1,'шт',2,'Мобильный пресс-компактор',0),
 ('eeeeeeee-4000-0000-0000-000000000003','eeeeeeee-3000-0000-0000-000000000003',1,'шт',4,'Знаки и указатели',0),
 ('eeeeeeee-4000-0000-0000-000000000004','eeeeeeee-3000-0000-0000-000000000004',1,'шт',5,'Пост охраны',0),
 ('eeeeeeee-4000-0000-0000-000000000005','eeeeeeee-3000-0000-0000-000000000005',1,'шт',1,'Насосная установка',0)
ON CONFLICT (id) DO NOTHING;

-- The MCP pricing target is ready for item pricing (explicit contractor quantity).
UPDATE public.client_positions SET manual_volume=2 WHERE id='eeeeeeee-4000-0000-0000-000000000001';

INSERT INTO public.boq_items(id,tender_id,client_position_id,sort_number,boq_item_type,material_type,material_name_id,work_name_id,unit_code,quantity,unit_rate,currency_type,delivery_price_type,delivery_amount,consumption_coefficient,total_amount,detail_cost_category_id,quote_link) VALUES
 ('eeeeeeee-5000-0000-0000-000000000001','eeeeeeee-3000-0000-0000-000000000002','eeeeeeee-4000-0000-0000-000000000002',1,'мат','основн.','eeeeeeee-1000-0000-0000-000000000001',NULL,'шт',2,2070000,'RUB','в цене',0,1,4140000,'eeeeeeee-0000-0000-0000-000000000002','КП-PRESSMAX-900'),
 ('eeeeeeee-5000-0000-0000-000000000002','eeeeeeee-3000-0000-0000-000000000002','eeeeeeee-4000-0000-0000-000000000002',2,'раб',NULL,NULL,'eeeeeeee-2000-0000-0000-000000000001','шт',2,1000,'RUB',NULL,0,NULL,2000,'eeeeeeee-0000-0000-0000-000000000002',NULL),
 ('eeeeeeee-5000-0000-0000-000000000003','eeeeeeee-3000-0000-0000-000000000002','eeeeeeee-4000-0000-0000-000000000002',3,'раб',NULL,NULL,'eeeeeeee-2000-0000-0000-000000000002','шт',2,900,'RUB',NULL,0,NULL,1800,'eeeeeeee-0000-0000-0000-000000000002',NULL),
 ('eeeeeeee-5000-0000-0000-000000000004','eeeeeeee-3000-0000-0000-000000000003','eeeeeeee-4000-0000-0000-000000000003',1,'мат','основн.','eeeeeeee-1000-0000-0000-000000000002',NULL,'шт',4,42000,'RUB','в цене',0,1,168000,'eeeeeeee-0000-0000-0000-000000000002','КП-GBO-42'),
 ('eeeeeeee-5000-0000-0000-000000000005','eeeeeeee-3000-0000-0000-000000000004','eeeeeeee-4000-0000-0000-000000000004',1,'мат','основн.','eeeeeeee-1000-0000-0000-000000000003',NULL,'шт',5,2500,'USD','в цене',0,1,1000000,'eeeeeeee-0000-0000-0000-000000000002','КП-SEC-USD'),
 ('eeeeeeee-5000-0000-0000-000000000006','eeeeeeee-3000-0000-0000-000000000005','eeeeeeee-4000-0000-0000-000000000005',1,'мат','основн.','eeeeeeee-1000-0000-0000-000000000004',NULL,'шт',1,1000,'EUR','в цене',0,1,90000,'eeeeeeee-0000-0000-0000-000000000002','КП-OLD-PUMP')
ON CONFLICT (id) DO NOTHING;

INSERT INTO public.works_library(id,work_name_id,item_type,unit_rate,currency_type)
VALUES ('eeeeeeee-6000-0000-0000-000000000001','eeeeeeee-2000-0000-0000-000000000003','раб',480,'RUB') ON CONFLICT (id) DO NOTHING;
INSERT INTO public.materials_library(id,material_type,item_type,consumption_coefficient,unit_rate,currency_type,delivery_price_type,delivery_amount,material_name_id)
VALUES ('eeeeeeee-6000-0000-0000-000000000002','основн.','мат',1,5049,'RUB','в цене',0,'eeeeeeee-1000-0000-0000-000000000005') ON CONFLICT (id) DO NOTHING;
INSERT INTO public.templates(id,name,detail_cost_category_id)
VALUES ('eeeeeeee-7000-0000-0000-000000000001','MCP EVAL — Колесоотбойник','eeeeeeee-0000-0000-0000-000000000002') ON CONFLICT (id) DO NOTHING;
INSERT INTO public.template_items(id,template_id,kind,work_library_id,position,detail_cost_category_id)
VALUES ('eeeeeeee-7100-0000-0000-000000000001','eeeeeeee-7000-0000-0000-000000000001','work','eeeeeeee-6000-0000-0000-000000000001',0,'eeeeeeee-0000-0000-0000-000000000002') ON CONFLICT (id) DO NOTHING;
INSERT INTO public.template_items(id,template_id,kind,material_library_id,parent_work_item_id,conversation_coeff,position,detail_cost_category_id)
VALUES ('eeeeeeee-7100-0000-0000-000000000002','eeeeeeee-7000-0000-0000-000000000001','material','eeeeeeee-6000-0000-0000-000000000002','eeeeeeee-7100-0000-0000-000000000001',1,1,'eeeeeeee-0000-0000-0000-000000000002') ON CONFLICT (id) DO NOTHING;

COMMIT;
SELECT 'MCP_EVALUATION_FIXTURE_OK' AS status;

