export type ScenarioStatus = 'draft' | 'evaluating' | 'pending_review' | 'approved' | 'archived';

export interface ConstraintViolation {
  code: string;
  severity: 'critical' | 'warning';
  entity_type: string;
  entity_id: number;
  message: string;
  actual: number;
  limit: number;
}

export interface RackAssignment {
  load_id: number;
  load_name: string;
  rack_id: number;
  rack_code: string;
  zone_id: number;
  zone_code: string;
  power_kw: number;
  heat_kw: number;
  airflow_cfm: number;
  rack_units: number;
  placement_score: number;
  explanation: string[];
}

export interface ZoneThermalResult {
  zone_id: number;
  zone_code: string;
  assigned_heat_kw: number;
  neighbor_heat_kw: number;
  estimated_return_c: number;
  temperature_margin_c: number;
  cooling_margin_kw: number;
}

export interface LayoutScenario {
  id: number;
  name: string;
  scenario_status: ScenarioStatus;
  load_ids: number[];
  evaluated: boolean;
  assignments: RackAssignment[];
  zone_results: ZoneThermalResult[];
  violations: ConstraintViolation[];
  total_power_kw: number;
  peak_temp_c: number;
  score: number;
  version: number;
  algorithm_version: string;
  created_by: number;
  approved_by: number | null;
  has_critical_violation: boolean;
}

export interface ScenarioComparison {
  left: LayoutScenario;
  right: LayoutScenario;
  score_delta: number;
  power_delta_kw: number;
  peak_temp_delta_c: number;
  summary: string[];
}

export interface RelocationUsage {
  power_kw: number;
  airflow_cfm: number;
  rack_units: number;
}

export interface RelocationRackTrial {
  rack_id: number;
  rack_code: string;
  zone_id: number;
  zone_code: string;
  rack_status: string;
  power_limit_kw: number;
  airflow_limit_cfm: number;
  rack_units_limit: number;
  occupied_before: RelocationUsage;
  vacated: RelocationUsage;
  projected: RelocationUsage & { heat_kw: number };
  power_headroom_kw: number;
  airflow_headroom_cfm: number;
  units_headroom: number;
}

export interface RelocationZoneTrial {
  zone_id: number;
  zone_code: string;
  zone_status: string;
  cooling_capacity_kw: number;
  assigned_heat_before_kw: number;
  remaining_heat_after_vacate_kw: number;
  projected_heat_kw: number;
  cooling_margin_kw: number;
  supply_temp_c: number;
  estimated_return_c: number;
  max_return_temp_c: number;
  temperature_margin_c: number;
}

export interface RelocationSource {
  has_placement: boolean;
  same_rack: boolean;
  rack_id: number;
  rack_code: string;
  zone_id: number;
  zone_code: string;
  remaining_after_vacate: RelocationUsage;
}

export interface RelocationTrial {
  scenario_id: number;
  algorithm_version: string;
  load: {
    id: number;
    name: string;
    power_kw: number;
    heat_kw: number;
    airflow_cfm: number;
    rack_units: number;
    redundancy_group: string;
  };
  source: RelocationSource;
  target_rack: RelocationRackTrial;
  target_zone: RelocationZoneTrial;
  violations: ConstraintViolation[];
  feasible: boolean;
  blocking_constraints: string[];
}
