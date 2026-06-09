package events

const (
	SpecVersion = "1.0"
	Source      = "iag-mes"

	TopicProduction  = "iag.production"
	TopicOperations  = "iag.operations"

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
)

func TopicForEvent(eventType string) string {
	switch eventType {
	case TypeDowntimeStarted, TypeDowntimeEnded, TypeWorkOrderDone:
		return TopicOperations
	default:
		return TopicProduction
	}
}
