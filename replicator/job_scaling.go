package replicator

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elsevier-core-engineering/replicator/client"
	"github.com/elsevier-core-engineering/replicator/logging"
	"github.com/elsevier-core-engineering/replicator/notifier"
	"github.com/elsevier-core-engineering/replicator/replicator/structs"
)

// newJobScalingPolicy returns a new JobScalingPolicies struct for Replicator
// to track and view Nomad job scaling.
func newJobScalingPolicy() *structs.JobScalingPolicies {

	return &structs.JobScalingPolicies{
		Lock:     sync.RWMutex{},
	}
}

func (r *Runner) asyncJobScaling(jobScalingPolicies *structs.JobScalingPolicies) {

	jobScalingPolicies.Policies.Range(func(key, _ interface{}) bool {
		job := key.(string)
		if r.config.NomadClient.IsJobInDeployment(job) {
			logging.Debug("core/job_scaling: job %s is in deployment, no scaling evaluation will be triggerd", job)
		} else {
			r.jobScaling(job, jobScalingPolicies)
		}
		return true
	})
}

func (r *Runner) jobScaling(jobID string, jobScalingPolicies *structs.JobScalingPolicies) {

	gi, ok := jobScalingPolicies.Policies.Load(jobID)
	if !ok {
		logging.Error("core/job_scaling: unable to find job [%s] in list of Policies", jobID)
		return
	}

	g := gi.([]*structs.GroupScalingPolicy)

	// Scaling a Cluster Jobs requires access to both Consul and Nomad therefore
	// we setup the clients here.
	nomadClient := r.config.NomadClient
	consulClient := r.config.ConsulClient

	// EvaluateJobScaling performs read/write to our map therefore we wrap it
	// in a read/write lock and remove this as soon as possible as the
	// remaining functions only need a read lock.
	jobScalingPolicies.Lock.Lock()
	err := nomadClient.EvaluateJobScaling(jobID, g)
	jobScalingPolicies.Lock.Unlock()

	// Horrible but required for jobs that have been purged as the policy
	// watcher will not get notified and such cannot remove the policy even
	// though the job doesn't exist. The string check is due to
	// github.com/hashicorp/nomad/issues/1849
	if err != nil && strings.Contains(err.Error(), "404") {
		client.RemoveJobScalingPolicy(jobID, jobScalingPolicies)
		return
	} else if err != nil {
		logging.Error("core/job_scaling: unable to perform job resource evaluation: %v", err)
		return
	}

	jobScalingPolicies.Lock.RLock()
	for _, group := range g {
		// Setup a failure message to pass to the failsafe check.
		message := &notifier.FailureMessage{
			AlertUID:     group.UID,
			ResourceID:   fmt.Sprintf("%s/%s", jobID, group.GroupName),
			ResourceType: JobType,
		}

		// Read or JobGroup state and check failsafe.
		s := &structs.ScalingState{
			ResourceName: group.GroupName,
			ResourceType: JobType,
			StatePath: r.config.ConsulKeyRoot + "/state/jobs/" + jobID +
				"/" + group.GroupName,
		}
		consulClient.ReadState(s, true)

		if !FailsafeCheck(s, r.config, 1, message) {
			logging.Error("core/job_scaling: job \"%v\" and group \"%v\" is in "+
				"failsafe mode", jobID, group.GroupName)
			continue
		}

		// Check the JobGroup scaling cooldown.
		cd := time.Duration(group.Cooldown) * time.Second

		if !s.LastScalingEvent.Before(time.Now().Add(-cd)) {
			logging.Debug("core/job_scaling: job \"%v\" and group \"%v\" has not reached scaling cooldown threshold of %s",
				jobID, group.GroupName, cd)
			continue
		}

		if group.ScaleDirection == client.ScalingDirectionOut || group.ScaleDirection == client.ScalingDirectionIn {
			if group.Enabled {
				logging.Debug("core/job_scaling: scaling for job \"%v\" and group \"%v\" is enabled; a "+
					"scaling operation (%v) will be requested", jobID, group.GroupName, group.ScaleDirection)

				// Submit the job and group for scaling.
				nomadClient.JobGroupScale(jobID, group, s)

			} else {
				logging.Debug("core/job_scaling: job scaling has been disabled; a "+
					"scaling operation (%v) would have been requested for \"%v\" "+
					"and group \"%v\"", group.ScaleDirection, jobID, group.GroupName)
			}
		}

		// Persist our state to Consul.
		consulClient.PersistState(s)

	}
	jobScalingPolicies.Lock.RUnlock()
}
