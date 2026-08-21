{{ config(materialized='table') }}

WITH
step_3_create_dynamic_loads_selected AS (
    SELECT s.*, 
        (s."customer_id") AS _ml_group_key,
        ('TITAN_LOAD') AS _ml_discriminator,
        ((NOT (COUNT(*) OVER (PARTITION BY (s."customer_id")) < 1))) AS _ml_guard_passed,
        ROW_NUMBER() OVER (PARTITION BY (s."customer_id") ORDER BY s."id") AS _ml_row_num,
        COUNT(*) OVER (PARTITION BY (s."customer_id")) AS _ml_progenitor_count,
        STRING_AGG(SUBSTRING(s."id" FROM 8), '' ORDER BY s."id") OVER (PARTITION BY (s."customer_id")) AS _ml_progenitor_hex,
        ('LOAD_TITAN_01') AS "_ml_assign_load_id",
        ('SIAC') AS "_ml_assign_shipper_id",
        ('STANDARD') AS "_ml_assign_freight_class",
        (SUM(s."total_charge_cents") OVER (PARTITION BY (s."customer_id"))) AS "_ml_assign_total_revenue_cents",
        ('NO') AS "_ml_assign_requires_permits",
        ('UNASSIGNED') AS "_ml_assign_status"
    FROM {{ ref('tx_02_enrich_titan_orders_raw_order') }} s WHERE ((s."operational_status" = 'CLIN')) AND s."is_active" = TRUE
),
step_3_create_dynamic_loads_new_entities AS (
    SELECT 
        ('sha256:' || ENCODE(DIGEST(
        '\x000000000000001f'::bytea
        || convert_to('maiden-lane.synthetic-entity.v1', 'UTF8')
        || decode(SUBSTRING(COALESCE(src."lineage_id", 'sha256:0000000000000000000000000000000000000000000000000000000000000000') FROM 8), 'hex')
        || decode(LPAD(TO_HEX(OCTET_LENGTH('dynamic_load')), 16, '0'), 'hex')
        || convert_to('dynamic_load', 'UTF8')
        || decode(LPAD(TO_HEX(OCTET_LENGTH('create_dynamic_loads')), 16, '0'), 'hex')
        || convert_to('create_dynamic_loads', 'UTF8')
        || (decode(LPAD(TO_HEX(src."_ml_progenitor_count"), 16, '0'), 'hex') || decode(src."_ml_progenitor_hex", 'hex'))
        || '\x01'::bytea
        || decode(LPAD(TO_HEX(OCTET_LENGTH((src."_ml_discriminator")::text)), 16, '0'), 'hex')
        || convert_to((src."_ml_discriminator")::text, 'UTF8'),
        'sha256'
    ), 'hex')) AS "id",
        src."lineage_id" AS "lineage_id",
        TRUE AS "is_active",
        src."_ml_assign_load_id" AS "load_id",
        src."_ml_assign_shipper_id" AS "shipper_id",
        src."_ml_assign_freight_class" AS "freight_class",
        src."_ml_assign_total_revenue_cents" AS "total_revenue_cents",
        src."_ml_assign_requires_permits" AS "requires_permits",
        src."_ml_assign_status" AS "status",
        'create_dynamic_loads' AS "updated_by_rule"
    FROM step_3_create_dynamic_loads_selected src
    WHERE src."_ml_guard_passed" = TRUE AND src."_ml_row_num" = 1
),
step_3_create_dynamic_loads_output_dynamic_load AS (
    SELECT t.* FROM {{ ref('stg_entities_dynamic_load') }} t
    UNION ALL
    SELECT n.* FROM step_3_create_dynamic_loads_new_entities n
)

SELECT * FROM step_3_create_dynamic_loads_output_dynamic_load
