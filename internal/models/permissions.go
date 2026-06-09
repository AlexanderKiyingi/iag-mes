package models

type PermissionDescriptor struct {
	Name        string
	Description string
}

func PermissionDescriptors() []PermissionDescriptor {
	return []PermissionDescriptor{
		{"mes.view_overview", "Dashboard / bootstrap"},
		{"mes.view_plant", "View plants and sections"},
		{"mes.change_plant", "Create or update plants and sections"},
		{"mes.view_asset", "View production assets"},
		{"mes.view_work_order", "View maintenance work orders"},
		{"mes.change_asset", "Create or update assets"},
		{"mes.add_work_order", "Create work orders"},
		{"mes.change_work_order", "Update work orders"},
		{"mes.complete_work_order", "Complete work orders"},
		{"mes.view_downtime", "View downtime events"},
		{"mes.add_downtime", "Log downtime events"},
		{"mes.view_kpi", "View KPI definitions and snapshots"},
		{"mes.view_alert", "View alerts and rules"},
		{"mes.ack_alert", "Acknowledge or resolve alerts"},
		{"mes.view_telemetry", "View latest asset telemetry"},
		{"mes.view_ai", "View AI recommendations"},
		{"mes.change_ai", "Accept or dismiss AI recommendations"},
		{"mes.view_energy", "View energy readings and insights"},
		{"mes.add_energy", "Record energy readings"},
		{"mes.admin.read", "View admin audit logs, config, and monitoring"},
		{"mes.admin.write", "Run MES integration syncs and background jobs"},
		{"mes.sync_integrations", "Legacy alias for mes.admin.write (integration sync)"},
	}
}
