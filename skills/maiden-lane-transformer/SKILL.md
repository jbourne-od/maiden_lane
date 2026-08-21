---
name: maiden-lane-transformer
description: Author, compile, execute, transpile to dbt/SQL, and verify Maiden Lane transformations and promotion gates for enterprise mapper replacement.
---

# Maiden Lane Transformation Skill

## Overview

Maiden Lane is a deterministic transformation system for compiling, executing, explaining, comparing, and gating mapper transformations. It completely replaces legacy mutable mapping scripts (`coreai`) with closed, statically analyzable `.ml` rules, exact provenance, target SQL/dbt transpilation, and 9-clause fail-closed promotion gating.

---

## 1. Authoring Maiden Lane Rules (`.ml` DSL)

Maiden Lane transformation rules are declared in `.ml` text files.

### 1.1 Syntax Structure

```
schema {
  entity <entity_kind> {
    <field_name>: <type> [optional];
  }
  relation <relation_kind> {
    from: <entity_kind>;
    to: <entity_kind>;
  }
}

rule <rule_id> {
  select <entity_kind>
  [where <boolean_predicate>]
  [group by <grouping_expr>]
  [guard <group_or_member_guard>]
  set <field_path> = <expr>;
}

checkpoint <checkpoint_key> after <rule_id>;
```

### 1.2 All 7 Structural Operators

1. **Select & Assign**:
   ```
   rule update_status {
     select driver
     where driver.hours >= 10
     set driver.status = "OVER_HOURS";
   }
   ```
2. **Insert Entity**:
   ```
   rule create_team {
     insert team from driver
     where driver.assignment_status == "ASSIGNED"
     discriminator driver.driver_id
     set team.depot = driver.depot;
   }
   ```
3. **Delete Entity**:
   ```
   rule delete_stale_trips {
     delete trip
     where trip.is_cancelled == true;
   }
   ```
4. **Relate Entities**:
   ```
   rule assign_driver_to_truck {
     relate assigned_truck from driver to truck
     where driver.depot == truck.depot;
   }
   ```
5. **Unrelate Entities**:
   ```
   rule unassign_driver {
     unrelate assigned_truck from driver to truck
     where driver.status == "OFF_DUTY";
   }
   ```
6. **Merge Entities**:
   ```
   rule merge_sub_orders {
     merge sub_order into master_order
     group by sub_order.customer_id
     discriminator master_order.customer_id
     set master_order.total_amount = sum(sub_order.amount);
   }
   ```
7. **Split Entity**:
   ```
   rule split_shift {
     split driver into shift_segment
     partition "morning" set shift_segment.period = "AM", shift_segment.hours = 4;
     partition "evening" set shift_segment.period = "PM", shift_segment.hours = 4;
   }
   ```

---

## 2. CLI Tooling Reference

Maiden Lane provides a unified CLI tool `bin/maiden-lane`:

### 2.1 Compile & Validate Rules Statically
```bash
bin/maiden-lane compile rules.ml [--schema schema.json] [--out plan.json]
```

### 2.2 Execute Against State Fixtures
```bash
# Execute with Reference Go Engine
bin/maiden-lane run rules.ml

# Execute with Target SQL CTE Engine
bin/maiden-lane run rules.ml --backend sql
```

### 2.3 Transpile to Target SQL or dbt Project
```bash
# Emit complete SQL CTE query pipeline
bin/maiden-lane transpile sql rules.ml

# Generate deployable dbt project directory
bin/maiden-lane transpile dbt rules.ml --out dbt_project/
```

### 2.4 Semantic State Diffing
```bash
bin/maiden-lane diff <baseline_state> <candidate_state>
```

### 2.5 9-Clause Promotion Gate
```bash
bin/maiden-lane gate
```

---

## 3. Invariants & Rules for AI Agents

1. **No Floating-Point Math**: All numeric calculations must use 64-bit integer values or fixed-scale decimals.
2. **Pure Determinisim**: No wall-clock times, ambient timezones, or unseeded random generators.
3. **Closed Syntax**: Do not inject arbitrary Python or raw SQL into `.ml` files.
4. **Fail-Closed Gate**: No artifact can be published unless all 9 promotion gate clauses pass.
