{{ config(materialized='view') }}

SELECT * FROM {{ ref('tx_04_assign_driver_to_load_relations') }}
