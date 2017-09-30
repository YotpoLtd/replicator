## 1.1.0 (Unreleased)

NEW FEATURES:

* **Scaling Provider**: Replicator now implements a cloud provider model to support cluster scaling operations across multiple cloud providers. [GH-203]

BUG FIXES:

* Filter out Nomad jobs that do not include an `update` stanza to prevent segmentation violations when attempting to verify job scaling activities. Thank you to @burdandrei. [GH-209]
* Update init command to generate correct cluster_scaling meta tags. Thank you to @burdandrei. [GH-210]
* Pass job scaling verification and processing methods the job ID rather than name to handle jobs where the name differs from the ID. Thank you to @burdandrei. [GH-212]
* Prevent processing of Nomad system jobs. [GH-220]

IMPROVEMENTS:

* Remove explicit min/max thresholds for cluster scaling in favor of dynamic threshold discovery in the cloud provider. [GH-211]
* Remove all internal constants used in favor of exported constants from the Nomad structs package. [GH-213]

## 1.0.3 (22 September 2017)

BUG FIXES:

* Reset accumulated job scaling values during each scaling evaluation. Thank you to @comebackoneyear and @lifesum. [GH-202]
* Average job utilization from all allocations, not just the last one processed. Thank you to @comebackoneyear and @lifesum. [GH-202]

## 1.0.2 (15 September 2017)

BUG FIXES:

* Improve and fix node protection mechanism. [GH-197]

## 1.0.1 (14 September 2017)

BUG FIXES:

* Reset failure count when exiting failsafe [GH-190]
* Retry when Nomad returns an empty deployment ID [GH-191]

IMPROVEMENTS:

* Improve accuracy and detail of cluster_scaling telemetry metrics. [GH-194]
* Improve accuracy and detail of job_scaling telemetry metrics. [GH-193]
* Improve logging output of cluster scaling operations when logging set to `INFO`. [GH-195]

## 1.0.0 (11 September 2017)

IMPROVEMENTS:

* Nomad JobScaling now uses meta parameters within the job file for configuration [GH-139]
* Replicator now tracks scaling deployment to confirm success [GH-146]
* Introduce Node Discovery With Dynamic Refresh [GH-148]
* Replicator now supports setting failsafe per job group [GH-152]
* Replicator now supports concurrent cluster scaling of multiple worker pools [GH-157]

## 0.0.2 (03 August 2017)

IMPROVEMENTS:

* Replicator now supports persistent storage of state tracking information in
Consul Key/Value stores. This allows the daemon to restart and gracefully
resume where it left off and supports graceful leadership changes. [GH-92]
* Scaling cooldown threshold is calculated and tracked internally rather than
externally referencing the worker pool autoscaling group. [GH-91]
* Failed worker nodes are detached after maximum retry interval is reached for
troubleshooting. [GH-76]
* Add support for event notifications via PagerDuty [GH-66]
* Replicator will send a notification if failsafe mode is enabled [GH-131]
* New nodes are verified to have successfully joined the worker pool after a
cluster scaling operation is initiated. Replicator will retry until a healthy
node is launched or the maximum retry threshold is reached. [GH-62]
* Allow Dry Run Operations Against Job Scaling Documents. [GH-53]
* Add the `scaling_interval config` parameter to replicator agent. [GH-49]
* Increase Default Cluster Scaling Cooldown Period to 600s. [GH-56]
* Addition of CLI flags for all configuration parameters. [GH-61]
* Add support for `dev` CLI flag [GH-63]
* AWS `TerminateInstance` function now verifies instance has successfully
transitioned to a terminated state. [GH-80]
* Make use of telemetry configuration by sending key metrics. [GH-85]
* Replicator now runs leadership locking using Consul sessions +  KV [GH-101]
* Introduce distributed failsafe mode and new failsafe CLI command [GH-105]
* `cluster-scaling-theshold` parameter is now used to determine scaling safety [GH-115]

BUG FIXES:

* The global job scaling enabled flag is now evaluated before allowing job
scaling operations. [GH-81]
* Prevent Divide By Zero Panic When Replicator Detects No Healthy Worker
Nodes. [GH-55]
* Prevent Cluster Scaling Operations That Would Violate ASG Constraints.
[GH-142]

## 0.0.1 (02 May 2017)

- Initial release.
