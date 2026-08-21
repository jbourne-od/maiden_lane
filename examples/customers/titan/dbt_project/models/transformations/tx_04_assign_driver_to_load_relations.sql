{{ config(materialized='table') }}

WITH
step_4_assign_driver_to_load_candidates AS (
    SELECT 
        'assigned_driver' AS "kind",
        'dynamic_driver' AS "from_kind",
        f."id" AS "from_id",
        'dynamic_load' AS "to_kind",
        t."id" AS "to_id",
        TRUE AS "is_active",
        'assign_driver_to_load' AS "updated_by_rule"
    FROM {{ ref('tx_01_create_dynamic_drivers_dynamic_driver') }} f
    CROSS JOIN {{ ref('tx_03_create_dynamic_loads_dynamic_load') }} t
    WHERE f."is_active" = TRUE AND ((f."status" = 'AVAILABLE'))
      AND t."is_active" = TRUE AND ((t."status" = 'UNASSIGNED'))
      AND ((f."status" = 'AVAILABLE')) = TRUE
),
step_4_assign_driver_to_load_output_relations AS (
    SELECT r.* FROM {{ ref('stg_relations') }} r
    UNION ALL
    SELECT c.* FROM step_4_assign_driver_to_load_candidates c
)

SELECT * FROM step_4_assign_driver_to_load_output_relations
