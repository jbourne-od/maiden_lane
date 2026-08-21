{{ config(materialized='table') }}

WITH
step_2_enrich_titan_orders_selected AS (
    SELECT m.* FROM {{ ref('stg_entities_raw_order') }} m WHERE ((m."operational_status" = 'CLIN')) AND m."is_active" = TRUE
),
step_2_enrich_titan_orders_qualified AS (
    SELECT s.*, 
        (s."order_id") AS _ml_group_key,
        ((NOT (COUNT(*) OVER (PARTITION BY (s."order_id")) < 1))) AS _ml_guard_passed
    FROM step_2_enrich_titan_orders_selected s
),
step_2_enrich_titan_orders_output_raw_order AS (
    SELECT 
        m."id",
        m."lineage_id",
        m."is_active",
        m."bill_distance_miles" AS "bill_distance_miles",
        m."commodity_id" AS "commodity_id",
        m."customer_id" AS "customer_id",
        CASE WHEN q."_ml_guard_passed" = TRUE THEN ((CASE WHEN (q."is_hazmat" = 'true') THEN 'HAZMAT' ELSE 'STANDARD' END)) ELSE m."freight_class" END AS "freight_class",
        m."is_hazmat" AS "is_hazmat",
        m."operational_status" AS "operational_status",
        m."order_id" AS "order_id",
        CASE WHEN q."_ml_guard_passed" = TRUE THEN ((CASE WHEN (45000 < q."weight_lbs") THEN 'YES' ELSE 'NO' END)) ELSE m."requires_permits" END AS "requires_permits",
        m."total_charge_cents" AS "total_charge_cents",
        m."weight_lbs" AS "weight_lbs",
        CASE WHEN q."_ml_guard_passed" = TRUE THEN 'enrich_titan_orders' ELSE m."updated_by_rule" END AS "updated_by_rule"
    FROM {{ ref('stg_entities_raw_order') }} m
    LEFT JOIN step_2_enrich_titan_orders_qualified q ON m."id" = q."id"
)

SELECT * FROM step_2_enrich_titan_orders_output_raw_order
