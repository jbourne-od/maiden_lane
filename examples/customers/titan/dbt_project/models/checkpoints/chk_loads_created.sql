{{ config(materialized='view') }}

SELECT * FROM {{ ref('tx_03_create_dynamic_loads_dynamic_load') }}
