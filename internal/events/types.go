package events

const (
	SpecVersion = "1.0"
	Source      = "iag-mes"

	TopicProduction = "iag.production"
	TopicOperations = "iag.operations"

	TypeWetmillStarted   = "mes.wetmill.started"
	TypeWetmillCompleted = "mes.wetmill.completed"
	TypeDryingStarted    = "mes.drying.started"
	TypeDryingCompleted  = "mes.drying.completed"
	TypeDrymillCompleted = "mes.drymill.completed"
	TypeRoastStarted     = "mes.roast.started"
	TypeRoastCompleted   = "mes.roast.completed"
	TypeStageAdvanced    = "mes.stage.advanced"
	TypeCCPRecorded      = "mes.ccp.recorded"
	TypeRunCompleted     = "mes.run.completed"

	TypeDowntimeStarted = "mes.downtime.started"
	TypeDowntimeEnded   = "mes.downtime.ended"
	TypeWorkOrderDone   = "mes.workorder.completed"
	// Asset registry changes, so iag-production keeps its machine
	// projection (prod_machines) current without reading this schema.
	TypeAssetCreated = "mes.asset.created"
	TypeAssetUpdated = "mes.asset.updated"
	// A machine that was removed rather than retired. Without this the
	// projection keeps a row for a machine that no longer exists: created and
	// updated were announced, deletion was not, so a tag deleted here stayed
	// listed in iag-production and stayed eligible for its KPI rollups.
	TypeAssetDeleted = "mes.asset.deleted"

	TypeAlertRaised = "mes.alert.raised"

	// TypeProcurementRequested asks iag-procurement to raise a requisition
	// (its consumer on iag.operations; origin_system = sourceService).
	TypeProcurementRequested = "procurement.requested"
)

func TopicForEvent(eventType string) string {
	switch eventType {
	case TypeDowntimeStarted, TypeDowntimeEnded, TypeWorkOrderDone, TypeAlertRaised,
		TypeAssetCreated, TypeAssetUpdated, TypeAssetDeleted,
		TypeProcurementRequested:
		return TopicOperations
	default:
		return TopicProduction
	}
}
