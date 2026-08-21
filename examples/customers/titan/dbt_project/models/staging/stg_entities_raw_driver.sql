{{ config(materialized='view') }}

SELECT * FROM {{ source('maiden_lane', 'raw_entities_raw_driver') }}
