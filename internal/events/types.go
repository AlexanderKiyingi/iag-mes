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

	TypeAlertRaised = "mes.alert.raised"
)

func TopicForEvent(eventType string) string {
	switch eventType {
	case TypeDowntimeStarted, TypeDowntimeEnded, TypeWorkOrderDone, TypeAlertRaised, TypeAssetCreated, TypeAssetUpdated:
		return TopicOperations
	default:
		return TopicProduction
	}
}
