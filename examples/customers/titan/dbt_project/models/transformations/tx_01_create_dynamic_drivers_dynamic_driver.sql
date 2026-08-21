{{ config(materialized='table') }}

WITH
step_1_create_dynamic_drivers_selected AS (
    SELECT s.*, 
        ('TITAN_DRIVER') AS _ml_discriminator,
        ((NOT (COUNT(*) < 1))) AS _ml_guard_passed
    FROM {{ ref('tx_00_enrich_titan_drivers_raw_driver') }} s WHERE ((s."is_active" = 'true')) AND s."is_active" = TRUE
),
step_1_create_dynamic_drivers_new_entities AS (
    SELECT 
        ('sha256:' || ENCODE(DIGEST('maiden-lane.synthetic-entity.v1\x00' || COALESCE(src."lineage_id", '') || ':' || 'dynamic_driver' || ':' || 'create_dynamic_drivers' || ':' || src."id" || ':' || COALESCE(src."_ml_discriminator"::text, ''), 'sha256'), 'hex')) AS "id",
        src."lineage_id" AS "lineage_id",
        TRUE AS "is_active",\n    ('DRIVER_TITAN_01') AS "driver_id",\n    ('17601') AS "home_zip",\n    (SUM(src."avl_drive_hours")) AS "remaining_drive_hours",\n    ('AVAILABLE') AS "status",
        'create_dynamic_drivers' AS "updated_by_rule"
    FROM step_1_create_dynamic_drivers_selected src
    WHERE src."_ml_guard_passed" = TRUE
),
step_1_create_dynamic_drivers_output_dynamic_driver AS (
    SELECT t.* FROM {{ ref('stg_entities_dynamic_driver') }} t
    UNION ALL
    SELECT n.* FROM step_1_create_dynamic_drivers_new_entities n
)

SELECT * FROM step_1_create_dynamic_drivers_output_dynamic_driver
