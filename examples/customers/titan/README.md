# Titan Transfer (McLeod TMS) - Live Customer Transformation Showcase

This example models a real-world enterprise customer: **Titan Transfer** running on **McLeod TMS**.

In the legacy Python mapper (`coreai`), Titan's transformations required over 2,500 lines of complex YAML (`mcleod_titan_map.yaml`) and procedural pandas code. 

In **Maiden Lane**, Titan's complete ingestion, classification, synthetic load creation, driver availability enrichment, and load assignment is expressed as a clean, statically checked `.ml` ruleset.

---

## 1. What This Example Demonstrates

1. **Raw Order Enrichment**:
   - Classifies freight class (`HAZMAT` vs `STANDARD`) and detects permit requirements for overweight loads (`> 45,000 lbs`).
2. **Raw Driver Duty & Availability**:
   - Calculates available drive/on-duty hours and classifies driver dispatch readiness (`READY` if $\ge 8$ hours, otherwise `REST_REQUIRED`).
3. **Synthetic Entity Insertion**:
   - Inserts certified `dynamic_load` entities aggregating revenue and `dynamic_driver` entities with canonical synthetic IDs.
4. **Relational Graph Linking**:
   - Relates available dynamic drivers to unassigned dynamic loads using explicit graph edges (`assigned_driver`).
5. **Multi-Level Completeness Profiles**:
   - `titan_cm_profile`: Lightweight readiness contract for Commitment Manager uploader.
   - `titan_optimizer_profile`: Strict multi-field contract for downstream Optimization.

---

## 2. Running the Live Demo

### 1. Compile & Statically Validate Ruleset
```bash
bin/maiden-lane compile examples/customers/titan/titan_orders.ml
```

### 2. Execute Transformation Against Real McLeod Ingestion Fixture
```bash
bin/maiden-lane run examples/customers/titan/titan_orders.ml --state examples/customers/titan/titan_input.json
```

### 3. Transpile into Target Postgres / Snowflake SQL CTE Pipeline
```bash
bin/maiden-lane transpile sql examples/customers/titan/titan_orders.ml
```

### 4. Transpile into Full Production dbt Project
```bash
bin/maiden-lane transpile dbt examples/customers/titan/titan_orders.ml --out ./titan_dbt_project
```
Inspect the generated dbt project:
```bash
ls -la ./titan_dbt_project/models/transformations/
```
