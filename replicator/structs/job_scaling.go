package structs

import (
	"sync"
	"golang.org/x/sync/syncmap"
	"time"
)

// NewGroupScalingPolicy is a constructor method that provides a pointer to a
// new group scaling policy object.
func NewGroupScalingPolicy() *GroupScalingPolicy {
	// Return a new group scaling policy object with default values set.
	return &GroupScalingPolicy{
		Cooldown: 60,
	}
}

// JobScalingPolicies tracks replicators view of Job scaling policies and states
// with a Lock to safe guard read/write/deletes to the Policies map.
type JobScalingPolicies struct {
	LastChangeIndex uint64
	Lock            sync.RWMutex
	Policies        syncmap.Map
}

// GroupScalingPolicy represents all the information needed to make
// JobTaskGroup scaling decisions.
type GroupScalingPolicy struct {
	Cooldown       time.Duration `mapstructure:"replicator_cooldown"`
	Enabled        bool          `mapstructure:"replicator_enabled"`
	GroupName      string
	Max            int            `mapstructure:"replicator_max"`
	Min            int            `mapstructure:"replicator_min"`
	ScaleDirection string         `hash:"ignore"`
	ScaleInCPU     float64        `mapstructure:"replicator_scalein_cpu"`
	ScaleInMem     float64        `mapstructure:"replicator_scalein_mem"`
	ScalingMetric  string         `hash:"ignore"`
	ScaleOutCPU    float64        `mapstructure:"replicator_scaleout_cpu"`
	ScaleOutMem    float64        `mapstructure:"replicator_scaleout_mem"`
	Tasks          TaskAllocation `hash:"ignore"`
	UID            string         `mapstructure:"replicator_notification_uid"`
}
