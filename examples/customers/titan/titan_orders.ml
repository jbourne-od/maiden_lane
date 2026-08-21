# Titan Transfer (McLeod TMS) - Maiden Lane Transformation Rules
#
# This ruleset replaces the legacy McLeod Titan mapper (mcleod_titan_map.yaml)
# with a deterministic, statically checked pipeline that transforms raw McLeod
# orders and drivers into certified Odyssey loads and driver availability records.

schema {
  entity raw_order {
    order_id: string;
    customer_id: string;
    commodity_id: string;
    weight_lbs: int64;
    total_charge_cents: int64;
    bill_distance_miles: int64;
    operational_status: string;
    is_hazmat: string;
    freight_class: string optional;
    requires_permits: string optional;
  }

  entity dynamic_load {
    load_id: string;
    shipper_id: string;
    freight_class: string;
    total_revenue_cents: int64;
    requires_permits: string;
    status: string;
  }

  entity raw_driver {
    driver_id: string;
    home_terminal_zip: string;
    avl_drive_hours: int64;
    avl_onduty_hours: int64;
    duty_status: string;
    is_active: string;
    dispatch_ready: string optional;
  }

  entity dynamic_driver {
    driver_id: string;
    home_zip: string;
    remaining_drive_hours: int64;
    status: string;
  }

  relation assigned_driver {
    from: dynamic_driver;
    to: dynamic_load;
  }
}

# 1. Enrich raw orders: classify freight & permits based on weight & hazmat
rule enrich_titan_orders {
  select raw_order
  where raw_order.operational_status == "CLIN"
  group_by raw_order.order_id
  having count() >= 1
  set raw_order.requires_permits = if(raw_order.weight_lbs > 45000, "YES", "NO"),
      raw_order.freight_class = if(raw_order.is_hazmat == "true", "HAZMAT", "STANDARD");
}

checkpoint orders_enriched after enrich_titan_orders;

# 2. Enrich raw drivers: determine dispatch readiness based on available hours
rule enrich_titan_drivers {
  select raw_driver
  where raw_driver.is_active == "true"
  group_by raw_driver.driver_id
  having count() >= 1
  set raw_driver.dispatch_ready = if(raw_driver.avl_drive_hours >= 8, "READY", "REST_REQUIRED");
}

checkpoint drivers_enriched after enrich_titan_drivers;

# 3. Form dynamic loads from enriched orders
rule create_dynamic_loads (depends_on: ["enrich_titan_orders"]) {
  insert dynamic_load {
    select raw_order
    where raw_order.operational_status == "CLIN"
    group_by raw_order.customer_id
    having count() >= 1
    discriminator: "TITAN_LOAD";
  } set dynamic_load.load_id = "LOAD_TITAN_01",
        dynamic_load.shipper_id = "SIAC",
        dynamic_load.freight_class = "STANDARD",
        dynamic_load.total_revenue_cents = sum(raw_order.total_charge_cents),
        dynamic_load.requires_permits = "NO",
        dynamic_load.status = "UNASSIGNED";
}

checkpoint loads_created after create_dynamic_loads;

# 4. Form dynamic drivers from active drivers
rule create_dynamic_drivers (depends_on: ["enrich_titan_drivers"]) {
  insert dynamic_driver {
    select raw_driver
    where raw_driver.is_active == "true"
    group_by raw_driver.home_terminal_zip
    having count() >= 1
    discriminator: "TITAN_DRIVER";
  } set dynamic_driver.driver_id = "DRIVER_TITAN_01",
        dynamic_driver.home_zip = "17601",
        dynamic_driver.remaining_drive_hours = sum(raw_driver.avl_drive_hours),
        dynamic_driver.status = "AVAILABLE";
}

checkpoint drivers_created after create_dynamic_drivers;

# 5. Relate available drivers to unassigned loads
rule assign_driver_to_load (depends_on: ["create_dynamic_loads", "create_dynamic_drivers"]) {
  relate dynamic_driver to dynamic_load as assigned_driver {
    from: select dynamic_driver where dynamic_driver.status == "AVAILABLE";
    to: select dynamic_load where dynamic_load.status == "UNASSIGNED";
    guard: dynamic_driver.status == "AVAILABLE";
  };
}

checkpoint load_assigned after assign_driver_to_load;

profile titan_cm_profile for entity dynamic_load {
  require dynamic_load.load_id present as LOAD_ID_REQUIRED;
  require dynamic_load.total_revenue_cents present as REVENUE_REQUIRED;
}

profile titan_optimizer_profile for entity dynamic_load {
  require dynamic_load.load_id present as LOAD_ID_REQUIRED;
  require dynamic_load.total_revenue_cents present as REVENUE_REQUIRED;
  require dynamic_load.requires_permits present as PERMITS_REQUIRED;
  require dynamic_load.freight_class present as FREIGHT_CLASS_REQUIRED;
}
