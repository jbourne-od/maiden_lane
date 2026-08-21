{{ config(materialized='view') }}

SELECT * FROM {{ ref('tx_02_enrich_titan_orders_raw_order') }}
