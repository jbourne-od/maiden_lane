{{ config(materialized='table') }}

WITH
step_1_create_dynamic_drivers_selected AS (
    SELECT s.*, 
        (s."home_terminal_zip") AS _ml_group_key,
        ('TITAN_DRIVER') AS _ml_discriminator,
        ((NOT (COUNT(*) OVER (PARTITION BY (s."home_terminal_zip")) < 1))) AS _ml_guard_passed,
        ROW_NUMBER() OVER (PARTITION BY (s."home_terminal_zip") ORDER BY s."id") AS _ml_row_num,
        COUNT(*) OVER (PARTITION BY (s."home_terminal_zip")) AS _ml_progenitor_count,
        STRING_AGG(SUBSTRING(s."id" FROM 8), '' ORDER BY s."id") OVER (PARTITION BY (s."home_terminal_zip")) AS _ml_progenitor_hex,
        ('DRIVER_TITAN_01') AS "_ml_assign_driver_id",
        ('17601') AS "_ml_assign_home_zip",
        (SUM(s."avl_drive_hours") OVER (PARTITION BY (s."home_terminal_zip"))) AS "_ml_assign_remaining_drive_hours",
        ('AVAILABLE') AS "_ml_assign_status"
    FROM {{ ref('tx_00_enrich_titan_drivers_raw_driver') }} s WHERE ((s."is_active" = 'true')) AND s."is_active" = TRUE
),
step_1_create_dynamic_drivers_new_entities AS (
    SELECT 
        ('sha256:' || ENCODE(DIGEST(
        '\x000000000000001f'::bytea
        || convert_to('maiden-lane.synthetic-entity.v1', 'UTF8')
        || decode(SUBSTRING(COALESCE(src."lineage_id", 'sha256:0000000000000000000000000000000000000000000000000000000000000000') FROM 8), 'hex')
        || decode(LPAD(TO_HEX(OCTET_LENGTH('dynamic_driver')), 16, '0'), 'hex')
        || convert_to('dynamic_driver', 'UTF8')
        || decode(LPAD(TO_HEX(OCTET_LENGTH('create_dynamic_drivers')), 16, '0'), 'hex')
        || convert_to('create_dynamic_drivers', 'UTF8')
        || (decode(LPAD(TO_HEX(src."_ml_progenitor_count"), 16, '0'), 'hex') || decode(src."_ml_progenitor_hex", 'hex'))
        || '\x01'::bytea
        || decode(LPAD(TO_HEX(OCTET_LENGTH((src."_ml_discriminator")::text)), 16, '0'), 'hex')
        || convert_to((src."_ml_discriminator")::text, 'UTF8'),
        'sha256'
    ), 'hex')) AS "id",
        src."lineage_id" AS "lineage_id",
        TRUE AS "is_active",
        src."_ml_assign_driver_id" AS "driver_id",
        src."_ml_assign_home_zip" AS "home_zip",
        src."_ml_assign_remaining_drive_hours" AS "remaining_drive_hours",
        src."_ml_assign_status" AS "status",
        'create_dynamic_drivers' AS "updated_by_rule"
    FROM step_1_create_dynamic_drivers_selected src
    WHERE src."_ml_guard_passed" = TRUE AND src."_ml_row_num" = 1
),
step_1_create_dynamic_drivers_output_dynamic_driver AS (
    SELECT t.* FROM {{ ref('stg_entities_dynamic_driver') }} t
    UNION ALL
    SELECT n.* FROM step_1_create_dynamic_drivers_new_entities n
)

SELECT * FROM step_1_create_dynamic_drivers_output_dynamic_driver
