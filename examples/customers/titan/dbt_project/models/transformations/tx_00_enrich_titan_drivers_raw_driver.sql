{{ config(materialized='table') }}

WITH
step_0_enrich_titan_drivers_selected AS (
    SELECT m.* FROM {{ ref('stg_entities_raw_driver') }} m WHERE ((m."is_active" = 'true')) AND m."is_active" = TRUE
),
step_0_enrich_titan_drivers_qualified AS (
    SELECT s.*, 
        (s."driver_id") AS _ml_group_key,
        ((NOT (COUNT(*) OVER (PARTITION BY (s."driver_id")) < 1))) AS _ml_guard_passed
    FROM step_0_enrich_titan_drivers_selected s
),
step_0_enrich_titan_drivers_output_raw_driver AS (
    SELECT 
        m."id",
        m."lineage_id",
        m."is_active",\n    m."avl_drive_hours" AS "avl_drive_hours",\n    m."avl_onduty_hours" AS "avl_onduty_hours",\n    CASE WHEN q."_ml_guard_passed" = TRUE THEN ((CASE WHEN (NOT (q."avl_drive_hours" < 8)) THEN 'READY' ELSE 'REST_REQUIRED' END)) ELSE m."dispatch_ready" END AS "dispatch_ready",\n    m."driver_id" AS "driver_id",\n    m."duty_status" AS "duty_status",\n    m."home_terminal_zip" AS "home_terminal_zip",\n    m."is_active" AS "is_active",
        CASE WHEN q."_ml_guard_passed" = TRUE THEN 'enrich_titan_drivers' ELSE m."updated_by_rule" END AS "updated_by_rule"
    FROM {{ ref('stg_entities_raw_driver') }} m
    LEFT JOIN step_0_enrich_titan_drivers_qualified q ON m."id" = q."id"
)

SELECT * FROM step_0_enrich_titan_drivers_output_raw_driver
