{{ config(materialized='view') }}

SELECT * FROM {{ ref('tx_00_enrich_titan_drivers_raw_driver') }}
