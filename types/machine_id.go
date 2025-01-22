package types

type MachineKey struct {
	MachineId   string `json:"machine_id" bson:"machine_id"`
	Project     string `json:"project" bson:"project"`
	ContainerId string `json:"container_id" bson:"container_id"`
}
