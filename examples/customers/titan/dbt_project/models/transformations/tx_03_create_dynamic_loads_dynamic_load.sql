{{ config(materialized='table') }}

WITH
step_3_create_dynamic_loads_selected AS (
    SELECT s.*, 
        ('TITAN_LOAD') AS _ml_discriminator,
        ((NOT (COUNT(*) < 1))) AS _ml_guard_passed
    FROM {{ ref('tx_02_enrich_titan_orders_raw_order') }} s WHERE ((s."operational_status" = 'CLIN')) AND s."is_active" = TRUE
),
step_3_create_dynamic_loads_new_entities AS (
    SELECT 
        ('sha256:' || ENCODE(DIGEST('maiden-lane.synthetic-entity.v1\x00' || COALESCE(src."lineage_id", '') || ':' || 'dynamic_load' || ':' || 'create_dynamic_loads' || ':' || src."id" || ':' || COALESCE(src."_ml_discriminator"::text, ''), 'sha256'), 'hex')) AS "id",
        src."lineage_id" AS "lineage_id",
        TRUE AS "is_active",\n    ('LOAD_TITAN_01') AS "load_id",\n    ('SIAC') AS "shipper_id",\n    ('STANDARD') AS "freight_class",\n    (SUM(src."total_charge_cents")) AS "total_revenue_cents",\n    ('NO') AS "requires_permits",\n    ('UNASSIGNED') AS "status",
        'create_dynamic_loads' AS "updated_by_rule"
    FROM step_3_create_dynamic_loads_selected src
    WHERE src."_ml_guard_passed" = TRUE
),
step_3_create_dynamic_loads_output_dynamic_load AS (
    SELECT t.* FROM {{ ref('stg_entities_dynamic_load') }} t
    UNION ALL
    SELECT n.* FROM step_3_create_dynamic_loads_new_entities n
)

SELECT * FROM step_3_create_dynamic_loads_output_dynamic_load
