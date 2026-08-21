{{ config(materialized='view') }}

SELECT * FROM {{ ref('tx_01_create_dynamic_drivers_dynamic_driver') }}
