package db_test

import (
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/creds"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/db/algorithm"
	"github.com/concourse/concourse/atc/event"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pipeline", func() {
	var (
		pipeline       db.Pipeline
		team           db.Team
		pipelineConfig atc.Config
		job            db.Job
	)

	BeforeEach(func() {
		var err error
		team, err = teamFactory.CreateTeam(atc.Team{Name: "some-team"})
		Expect(err).ToNot(HaveOccurred())

		pipelineConfig = atc.Config{
			Groups: atc.GroupConfigs{
				{
					Name:      "some-group",
					Jobs:      []string{"job-1", "job-2"},
					Resources: []string{"some-resource", "some-other-resource"},
				},
			},
			Jobs: atc.JobConfigs{
				{
					Name: "job-name",

					Public: true,

					Serial: true,

					SerialGroups: []string{"serial-group"},

					Plan: atc.PlanSequence{
						{
							Put: "some-resource",
							Params: atc.Params{
								"some-param": "some-value",
							},
						},
						{
							Get:      "some-input",
							Resource: "some-resource",
							Params: atc.Params{
								"some-param": "some-value",
							},
							Passed:  []string{"job-1", "job-2"},
							Trigger: true,
						},
						{
							Task:           "some-task",
							Privileged:     true,
							TaskConfigPath: "some/config/path.yml",
							TaskConfig: &atc.TaskConfig{
								RootfsURI: "some-image",
							},
						},
					},
				},
				{
					Name:   "some-other-job",
					Serial: true,
				},
				{
					Name: "a-job",
				},
				{
					Name: "shared-job",
				},
				{
					Name: "random-job",
				},
				{
					Name:         "other-serial-group-job",
					SerialGroups: []string{"serial-group", "really-different-group"},
				},
				{
					Name:         "different-serial-group-job",
					SerialGroups: []string{"different-serial-group"},
				},
			},
			Resources: atc.ResourceConfigs{
				{
					Name:   "some-resource",
					Type:   "some-type",
					Source: atc.Source{"some": "source"},
				},
				{
					Name:   "some-other-resource",
					Type:   "some-type",
					Source: atc.Source{"some": "other-source"},
				},
			},
			ResourceTypes: atc.ResourceTypes{
				{
					Name:   "some-resource-type",
					Type:   "base-type",
					Source: atc.Source{"some": "type-soure"},
				},
				{
					Name:   "some-other-resource-type",
					Type:   "base-type",
					Source: atc.Source{"some": "other-type-soure"},
				},
			},
		}
		var created bool
		pipeline, created, err = team.SavePipeline("fake-pipeline", pipelineConfig, db.ConfigVersion(0), db.PipelineUnpaused)
		Expect(err).ToNot(HaveOccurred())
		Expect(created).To(BeTrue())

		var found bool
		job, found, err = pipeline.Job("job-name")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeTrue())

		setupTx, err := dbConn.Begin()
		Expect(err).ToNot(HaveOccurred())

		brt := db.BaseResourceType{
			Name: "some-type",
		}
		_, err = brt.FindOrCreate(setupTx)
		Expect(err).NotTo(HaveOccurred())
		Expect(setupTx.Commit()).To(Succeed())
	})

	Describe("CheckPaused", func() {
		var paused bool
		JustBeforeEach(func() {
			var err error
			paused, err = pipeline.CheckPaused()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when the pipeline is unpaused", func() {
			BeforeEach(func() {
				Expect(pipeline.Unpause()).To(Succeed())
			})

			It("returns the pipeline is paused", func() {
				Expect(paused).To(BeFalse())
			})
		})

		Context("when the pipeline is paused", func() {
			BeforeEach(func() {
				Expect(pipeline.Pause()).To(Succeed())
			})

			It("returns the pipeline is paused", func() {
				Expect(paused).To(BeTrue())
			})
		})
	})

	Describe("Pause", func() {
		JustBeforeEach(func() {
			Expect(pipeline.Pause()).To(Succeed())

			found, err := pipeline.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
		})

		Context("when the pipeline is unpaused", func() {
			BeforeEach(func() {
				Expect(pipeline.Unpause()).To(Succeed())
			})

			It("pauses the pipeline", func() {
				Expect(pipeline.Paused()).To(BeTrue())
			})
		})
	})

	Describe("Unpause", func() {
		JustBeforeEach(func() {
			Expect(pipeline.Unpause()).To(Succeed())

			found, err := pipeline.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
		})

		Context("when the pipeline is paused", func() {
			BeforeEach(func() {
				Expect(pipeline.Pause()).To(Succeed())
			})

			It("unpauses the pipeline", func() {
				Expect(pipeline.Paused()).To(BeFalse())
			})
		})
	})

	Describe("Rename", func() {
		JustBeforeEach(func() {
			Expect(pipeline.Rename("oopsies")).To(Succeed())
		})

		It("renames the pipeline", func() {
			pipeline, found, err := team.Pipeline("oopsies")
			Expect(pipeline.Name()).To(Equal("oopsies"))
			Expect(found).To(BeTrue())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Resource Config Versions", func() {
		resourceName := "some-resource"
		otherResourceName := "some-other-resource"
		reallyOtherResourceName := "some-really-other-resource"

		var (
			dbPipeline                db.Pipeline
			otherDBPipeline           db.Pipeline
			resource                  db.Resource
			otherResource             db.Resource
			reallyOtherResource       db.Resource
			resourceConfig            db.ResourceConfig
			otherResourceConfig       db.ResourceConfig
			reallyOtherResourceConfig db.ResourceConfig
			otherPipelineResource     db.Resource
		)

		BeforeEach(func() {
			pipelineConfig := atc.Config{
				Groups: atc.GroupConfigs{
					{
						Name:      "some-group",
						Jobs:      []string{"job-1", "job-2"},
						Resources: []string{"some-resource", "some-other-resource"},
					},
				},

				Resources: atc.ResourceConfigs{
					{
						Name: "some-resource",
						Type: "some-type",
						Source: atc.Source{
							"source-config": "some-value",
						},
					},
					{
						Name: "some-other-resource",
						Type: "some-type",
						Source: atc.Source{
							"source-config": "some-other-value",
						},
					},
					{
						Name: "some-really-other-resource",
						Type: "some-type",
						Source: atc.Source{
							"source-config": "some-really-other-value",
						},
					},
				},

				ResourceTypes: atc.ResourceTypes{
					{
						Name: "some-resource-type",
						Type: "some-type",
						Source: atc.Source{
							"source-config": "some-value",
						},
					},
				},

				Jobs: atc.JobConfigs{
					{
						Name: "some-job",

						Public: true,

						Serial: true,

						SerialGroups: []string{"serial-group"},

						Plan: atc.PlanSequence{
							{
								Put: "some-resource",
								Params: atc.Params{
									"some-param": "some-value",
								},
							},
							{
								Get:      "some-input",
								Resource: "some-resource",
								Params: atc.Params{
									"some-param": "some-value",
								},
								Passed:  []string{"job-1", "job-2"},
								Trigger: true,
							},
							{
								Task:           "some-task",
								Privileged:     true,
								TaskConfigPath: "some/config/path.yml",
								TaskConfig: &atc.TaskConfig{
									RootfsURI: "some-image",
								},
							},
						},
					},
					{
						Name:   "some-other-job",
						Serial: true,
					},
					{
						Name: "a-job",
					},
					{
						Name: "shared-job",
					},
					{
						Name: "random-job",
					},
					{
						Name:         "other-serial-group-job",
						SerialGroups: []string{"serial-group", "really-different-group"},
					},
					{
						Name:         "different-serial-group-job",
						SerialGroups: []string{"different-serial-group"},
					},
				},
			}

			otherPipelineConfig := atc.Config{
				Groups: atc.GroupConfigs{
					{
						Name:      "some-group",
						Jobs:      []string{"job-1", "job-2"},
						Resources: []string{"some-resource", "some-other-resource"},
					},
				},

				Resources: atc.ResourceConfigs{
					{
						Name: "some-resource",
						Type: "some-type",
						Source: atc.Source{
							"other-source-config": "some-value",
						},
					},
					{
						Name: "some-other-resource",
						Type: "some-type",
						Source: atc.Source{
							"other-source-config": "some-other-value",
						},
					},
				},

				Jobs: atc.JobConfigs{
					{
						Name: "some-job",
					},
					{
						Name: "some-other-job",
					},
					{
						Name: "a-job",
					},
					{
						Name: "shared-job",
					},
					{
						Name: "other-serial-group-job",
					},
				},
			}

			var err error
			dbPipeline, _, err = team.SavePipeline("pipeline-name", pipelineConfig, 0, db.PipelineUnpaused)
			Expect(err).ToNot(HaveOccurred())

			otherDBPipeline, _, err = team.SavePipeline("other-pipeline-name", otherPipelineConfig, 0, db.PipelineUnpaused)
			Expect(err).ToNot(HaveOccurred())

			resource, _, err = dbPipeline.Resource(resourceName)
			Expect(err).ToNot(HaveOccurred())

			otherResource, _, err = dbPipeline.Resource(otherResourceName)
			Expect(err).ToNot(HaveOccurred())

			reallyOtherResource, _, err = dbPipeline.Resource(reallyOtherResourceName)
			Expect(err).ToNot(HaveOccurred())

			otherPipelineResource, _, err = otherDBPipeline.Resource(otherResourceName)
			Expect(err).ToNot(HaveOccurred())

			resourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"source-config": "some-value"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			otherResourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"other-source-config": "some-other-value"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			reallyOtherResourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"source-config": "some-really-other-value"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			err = resource.SetResourceConfig(resourceConfig.ID())
			Expect(err).ToNot(HaveOccurred())

			err = otherPipelineResource.SetResourceConfig(otherResourceConfig.ID())
			Expect(err).ToNot(HaveOccurred())

			err = reallyOtherResource.SetResourceConfig(reallyOtherResourceConfig.ID())
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns correct resource", func() {
			Expect(resource.Name()).To(Equal("some-resource"))
			Expect(resource.PipelineName()).To(Equal("pipeline-name"))
			Expect(resource.CheckError()).To(BeNil())
			Expect(resource.Type()).To(Equal("some-type"))
			Expect(resource.Source()).To(Equal(atc.Source{"source-config": "some-value"}))
		})

		It("can load up resource config version information relevant to scheduling", func() {
			job, found, err := dbPipeline.Job("some-job")
			Expect(found).To(BeTrue())
			Expect(err).ToNot(HaveOccurred())

			otherJob, found, err := dbPipeline.Job("some-other-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			aJob, found, err := dbPipeline.Job("a-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			sharedJob, found, err := dbPipeline.Job("shared-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			randomJob, found, err := dbPipeline.Job("random-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			otherSerialGroupJob, found, err := dbPipeline.Job("other-serial-group-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			differentSerialGroupJob, found, err := dbPipeline.Job("different-serial-group-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			versions, err := dbPipeline.LoadVersionsDB()
			Expect(err).ToNot(HaveOccurred())
			Expect(versions.ResourceVersions).To(BeEmpty())
			Expect(versions.BuildOutputs).To(BeEmpty())
			Expect(versions.ResourceIDs).To(Equal(map[string]int{
				resource.Name():            resource.ID(),
				otherResource.Name():       otherResource.ID(),
				reallyOtherResource.Name(): reallyOtherResource.ID(),
			}))

			Expect(versions.JobIDs).To(Equal(map[string]int{
				"some-job":                   job.ID(),
				"some-other-job":             otherJob.ID(),
				"a-job":                      aJob.ID(),
				"shared-job":                 sharedJob.ID(),
				"random-job":                 randomJob.ID(),
				"other-serial-group-job":     otherSerialGroupJob.ID(),
				"different-serial-group-job": differentSerialGroupJob.ID(),
			}))

			By("initially having no latest versioned resource")
			latestVersions, err := resourceConfig.LatestVersions()
			Expect(err).ToNot(HaveOccurred())
			Expect(latestVersions).To(HaveLen(0))

			By("including saved versioned resources of the current pipeline")
			saveVersions(resourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "1"},
					Space:   atc.Space("space"),
				},
			})

			err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "1"})
			Expect(err).ToNot(HaveOccurred())

			savedVR1, err := resourceConfig.LatestVersions()
			Expect(err).ToNot(HaveOccurred())
			Expect(savedVR1).To(HaveLen(1))

			saveVersions(resourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "2"},
					Space:   atc.Space("space"),
				},
			})

			err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "2"})
			Expect(err).ToNot(HaveOccurred())

			savedVR2, err := resourceConfig.LatestVersions()
			Expect(err).ToNot(HaveOccurred())
			Expect(savedVR2).To(HaveLen(1))

			versions, err = dbPipeline.LoadVersionsDB()
			Expect(err).ToNot(HaveOccurred())
			Expect(versions.ResourceVersions).To(ConsistOf([]algorithm.ResourceVersion{
				{VersionID: savedVR1[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR1[0].CheckOrder()},
				{VersionID: savedVR2[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR2[0].CheckOrder()},
			}))

			Expect(versions.BuildOutputs).To(BeEmpty())
			Expect(versions.ResourceIDs).To(Equal(map[string]int{
				resource.Name():            resource.ID(),
				otherResource.Name():       otherResource.ID(),
				reallyOtherResource.Name(): reallyOtherResource.ID(),
			}))

			Expect(versions.JobIDs).To(Equal(map[string]int{
				"some-job":                   job.ID(),
				"some-other-job":             otherJob.ID(),
				"a-job":                      aJob.ID(),
				"shared-job":                 sharedJob.ID(),
				"random-job":                 randomJob.ID(),
				"other-serial-group-job":     otherSerialGroupJob.ID(),
				"different-serial-group-job": differentSerialGroupJob.ID(),
			}))

			By("not including saved versioned resources of other pipelines")
			saveVersions(otherResourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "1"},
					Space:   atc.Space("space"),
				},
			})

			err = otherResourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "1"})
			Expect(err).ToNot(HaveOccurred())

			latestVersions, err = otherResourceConfig.LatestVersions()
			Expect(err).ToNot(HaveOccurred())
			Expect(latestVersions).ToNot(BeEmpty())

			versions, err = dbPipeline.LoadVersionsDB()
			Expect(err).ToNot(HaveOccurred())
			Expect(versions.ResourceVersions).To(ConsistOf([]algorithm.ResourceVersion{
				{VersionID: savedVR1[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR1[0].CheckOrder()},
				{VersionID: savedVR2[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR2[0].CheckOrder()},
			}))

			Expect(versions.BuildOutputs).To(BeEmpty())
			Expect(versions.ResourceIDs).To(Equal(map[string]int{
				resource.Name():            resource.ID(),
				otherResource.Name():       otherResource.ID(),
				reallyOtherResource.Name(): reallyOtherResource.ID(),
			}))

			Expect(versions.JobIDs).To(Equal(map[string]int{
				"some-job":                   job.ID(),
				"some-other-job":             otherJob.ID(),
				"a-job":                      aJob.ID(),
				"shared-job":                 sharedJob.ID(),
				"random-job":                 randomJob.ID(),
				"other-serial-group-job":     otherSerialGroupJob.ID(),
				"different-serial-group-job": differentSerialGroupJob.ID(),
			}))

			By("including outputs of successful builds")
			build1DB, err := aJob.CreateBuild()
			Expect(err).ToNot(HaveOccurred())

			err = build1DB.SaveOutput(resourceConfig, atc.SpaceVersion{
				Space:   atc.Space("space"),
				Version: atc.Version{"version": "1"},
			}, "some-output-name", "some-resource")
			Expect(err).ToNot(HaveOccurred())

			err = build1DB.Finish(db.BuildStatusSucceeded)
			Expect(err).ToNot(HaveOccurred())

			versions, err = dbPipeline.LoadVersionsDB()
			Expect(err).ToNot(HaveOccurred())
			Expect(versions.ResourceVersions).To(ConsistOf([]algorithm.ResourceVersion{
				{VersionID: savedVR1[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR1[0].CheckOrder()},
				{VersionID: savedVR2[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR2[0].CheckOrder()},
			}))

			explicitOutput := algorithm.BuildOutput{
				ResourceVersion: algorithm.ResourceVersion{
					VersionID:  savedVR1[0].ID(),
					ResourceID: resource.ID(),
					CheckOrder: savedVR1[0].CheckOrder(),
				},
				JobID:   aJob.ID(),
				BuildID: build1DB.ID(),
			}

			Expect(versions.BuildOutputs).To(ConsistOf([]algorithm.BuildOutput{
				explicitOutput,
			}))

			Expect(versions.ResourceIDs).To(Equal(map[string]int{
				resource.Name():            resource.ID(),
				otherResource.Name():       otherResource.ID(),
				reallyOtherResource.Name(): reallyOtherResource.ID(),
			}))

			Expect(versions.JobIDs).To(Equal(map[string]int{
				"some-job":                   job.ID(),
				"a-job":                      aJob.ID(),
				"some-other-job":             otherJob.ID(),
				"shared-job":                 sharedJob.ID(),
				"random-job":                 randomJob.ID(),
				"other-serial-group-job":     otherSerialGroupJob.ID(),
				"different-serial-group-job": differentSerialGroupJob.ID(),
			}))

			By("not including outputs of failed builds")
			build2DB, err := aJob.CreateBuild()
			Expect(err).ToNot(HaveOccurred())

			err = build2DB.SaveOutput(resourceConfig, atc.SpaceVersion{
				Space:   atc.Space("space"),
				Version: atc.Version{"version": "1"},
			}, "some-output-name", "some-resource")
			Expect(err).ToNot(HaveOccurred())

			err = build2DB.Finish(db.BuildStatusFailed)
			Expect(err).ToNot(HaveOccurred())

			versions, err = dbPipeline.LoadVersionsDB()
			Expect(err).ToNot(HaveOccurred())
			Expect(versions.ResourceVersions).To(ConsistOf([]algorithm.ResourceVersion{
				{VersionID: savedVR1[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR1[0].CheckOrder()},
				{VersionID: savedVR2[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR2[0].CheckOrder()},
			}))

			Expect(versions.BuildOutputs).To(ConsistOf([]algorithm.BuildOutput{
				{
					ResourceVersion: algorithm.ResourceVersion{
						VersionID:  savedVR1[0].ID(),
						ResourceID: resource.ID(),
						CheckOrder: savedVR1[0].CheckOrder(),
					},
					JobID:   aJob.ID(),
					BuildID: build1DB.ID(),
				},
			}))

			Expect(versions.ResourceIDs).To(Equal(map[string]int{
				resource.Name():            resource.ID(),
				otherResource.Name():       otherResource.ID(),
				reallyOtherResource.Name(): reallyOtherResource.ID(),
			}))

			Expect(versions.JobIDs).To(Equal(map[string]int{
				"some-job":                   job.ID(),
				"a-job":                      aJob.ID(),
				"some-other-job":             otherJob.ID(),
				"shared-job":                 sharedJob.ID(),
				"random-job":                 randomJob.ID(),
				"other-serial-group-job":     otherSerialGroupJob.ID(),
				"different-serial-group-job": differentSerialGroupJob.ID(),
			}))

			By("not including outputs of builds in other pipelines")
			anotherJob, found, err := otherDBPipeline.Job("a-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			otherPipelineBuild, err := anotherJob.CreateBuild()
			Expect(err).ToNot(HaveOccurred())

			err = otherPipelineBuild.SaveOutput(otherResourceConfig, atc.SpaceVersion{
				Space:   atc.Space("space"),
				Version: atc.Version{"version": "1"},
			}, "some-output-name", "some-other-resource")
			Expect(err).ToNot(HaveOccurred())

			err = otherPipelineBuild.Finish(db.BuildStatusSucceeded)
			Expect(err).ToNot(HaveOccurred())

			versions, err = dbPipeline.LoadVersionsDB()
			Expect(err).ToNot(HaveOccurred())
			Expect(versions.ResourceVersions).To(ConsistOf([]algorithm.ResourceVersion{
				{VersionID: savedVR1[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR1[0].CheckOrder()},
				{VersionID: savedVR2[0].ID(), ResourceID: resource.ID(), CheckOrder: savedVR2[0].CheckOrder()},
			}))

			Expect(versions.BuildOutputs).To(ConsistOf([]algorithm.BuildOutput{
				{
					ResourceVersion: algorithm.ResourceVersion{
						VersionID:  savedVR1[0].ID(),
						ResourceID: resource.ID(),
						CheckOrder: savedVR1[0].CheckOrder(),
					},
					JobID:   aJob.ID(),
					BuildID: build1DB.ID(),
				},
			}))

			Expect(versions.ResourceIDs).To(Equal(map[string]int{
				resource.Name():            resource.ID(),
				otherResource.Name():       otherResource.ID(),
				reallyOtherResource.Name(): reallyOtherResource.ID(),
			}))

			Expect(versions.JobIDs).To(Equal(map[string]int{
				"some-job":                   job.ID(),
				"a-job":                      aJob.ID(),
				"some-other-job":             otherJob.ID(),
				"shared-job":                 sharedJob.ID(),
				"random-job":                 randomJob.ID(),
				"other-serial-group-job":     otherSerialGroupJob.ID(),
				"different-serial-group-job": differentSerialGroupJob.ID(),
			}))

			By("including build inputs")
			aJob, found, err = dbPipeline.Job("a-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			build1DB, err = aJob.CreateBuild()
			Expect(err).ToNot(HaveOccurred())

			err = build1DB.UseInputs([]db.BuildInput{
				db.BuildInput{
					Name:       "some-input-name",
					Version:    atc.Version{"version": "1"},
					Space:      atc.Space("space"),
					ResourceID: resource.ID(),
				},
			})
			Expect(err).ToNot(HaveOccurred())

			err = build1DB.Finish(db.BuildStatusSucceeded)
			Expect(err).ToNot(HaveOccurred())

			versions, err = dbPipeline.LoadVersionsDB()
			Expect(err).ToNot(HaveOccurred())

			Expect(versions.BuildInputs).To(ConsistOf([]algorithm.BuildInput{
				{
					ResourceVersion: algorithm.ResourceVersion{
						VersionID:  savedVR1[0].ID(),
						ResourceID: resource.ID(),
						CheckOrder: savedVR1[0].CheckOrder(),
					},
					JobID:     aJob.ID(),
					BuildID:   build1DB.ID(),
					InputName: "some-input-name",
				},
			}))

			By("including implicit outputs of successful builds")
			implicitOutput := algorithm.BuildOutput{
				ResourceVersion: algorithm.ResourceVersion{
					VersionID:  savedVR1[0].ID(),
					ResourceID: resource.ID(),
					CheckOrder: savedVR1[0].CheckOrder(),
				},
				JobID:   aJob.ID(),
				BuildID: build1DB.ID(),
			}

			Expect(versions.BuildOutputs).To(ConsistOf([]algorithm.BuildOutput{
				explicitOutput,
				implicitOutput,
			}))
		})

		It("can load up the latest versioned resource, enabled or not", func() {
			By("initially having no latest versioned resource")
			latestVersion, err := resourceConfig.LatestVersions()
			Expect(err).ToNot(HaveOccurred())
			Expect(latestVersion).To(BeEmpty())

			By("including saved versioned resources of the current pipeline")
			saveVersions(resourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "1"},
					Space:   atc.Space("space"),
				},
			})

			err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "1"})
			Expect(err).ToNot(HaveOccurred())

			savedVR1, err := resourceConfig.LatestVersions()
			Expect(err).ToNot(HaveOccurred())
			Expect(savedVR1).ToNot(BeEmpty())

			saveVersions(resourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "2"},
					Space:   atc.Space("space"),
				},
			})

			err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "2"})
			Expect(err).ToNot(HaveOccurred())

			savedVR2, err := resourceConfig.LatestVersions()
			Expect(err).ToNot(HaveOccurred())
			Expect(savedVR2).ToNot(BeEmpty())

			Expect(savedVR1[0].Version()).To(Equal(db.Version{"version": "1"}))
			Expect(savedVR2[0].Version()).To(Equal(db.Version{"version": "2"}))

			By("not including saved versioned resources of other pipelines")
			_, _, err = otherDBPipeline.Resource("some-other-resource")
			Expect(err).ToNot(HaveOccurred())

			saveVersions(otherResourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "1"},
					Space:   atc.Space("space"),
				},
				atc.SpaceVersion{
					Version: atc.Version{"version": "2"},
					Space:   atc.Space("space"),
				},
				atc.SpaceVersion{
					Version: atc.Version{"version": "3"},
					Space:   atc.Space("space"),
				},
			})

			err = otherResourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "3"})
			Expect(err).ToNot(HaveOccurred())

			otherPipelineSavedVR, err := otherResourceConfig.LatestVersions()
			Expect(err).ToNot(HaveOccurred())
			Expect(otherPipelineSavedVR).ToNot(BeEmpty())

			Expect(otherPipelineSavedVR[0].Version()).To(Equal(db.Version{"version": "3"}))

			By("including disabled versions")
			err = resource.DisableVersion(savedVR2[0].ID())
			Expect(err).ToNot(HaveOccurred())

			latestVR, err := resourceConfig.LatestVersions()
			Expect(err).ToNot(HaveOccurred())
			Expect(latestVR).ToNot(BeEmpty())

			Expect(latestVR[0].Version()).To(Equal(db.Version{"version": "2"}))
		})

		Describe("enabling and disabling versioned resources", func() {
			It("returns an error if the version is bogus", func() {
				err := resource.EnableVersion(42)
				Expect(err).To(HaveOccurred())

				err = resource.DisableVersion(42)
				Expect(err).To(HaveOccurred())
			})

			It("does not affect explicitly fetching the latest version", func() {
				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "1"},
						Space:   atc.Space("space"),
					},
				})

				err := resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "1"})
				Expect(err).ToNot(HaveOccurred())

				savedRCV, err := resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(savedRCV).ToNot(BeEmpty())
				Expect(savedRCV[0].Version()).To(Equal(db.Version{"version": "1"}))

				err = resource.DisableVersion(savedRCV[0].ID())
				Expect(err).ToNot(HaveOccurred())

				latestVR, err := resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(latestVR).ToNot(BeEmpty())
				Expect(latestVR[0].Version()).To(Equal(db.Version{"version": "1"}))

				err = resource.EnableVersion(savedRCV[0].ID())
				Expect(err).ToNot(HaveOccurred())

				latestVR, err = resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(latestVR).ToNot(BeEmpty())
				Expect(latestVR[0].Version()).To(Equal(db.Version{"version": "1"}))
			})

			It("doesn't change the check_order when saving a new build input", func() {
				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "1"},
						Space:   atc.Space("space"),
					},
					atc.SpaceVersion{
						Version: atc.Version{"version": "2"},
						Space:   atc.Space("space"),
					},
					atc.SpaceVersion{
						Version: atc.Version{"version": "3"},
						Space:   atc.Space("space"),
					},
				})

				job, found, err := dbPipeline.Job("some-job")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())

				build, err := job.CreateBuild()
				Expect(err).ToNot(HaveOccurred())

				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "4"},
						Space:   atc.Space("space"),
					},
					atc.SpaceVersion{
						Version: atc.Version{"version": "5"},
						Space:   atc.Space("space"),
					},
				})

				input := db.BuildInput{
					Name:       "input-name",
					Version:    atc.Version{"version": "3"},
					Space:      atc.Space("space"),
					ResourceID: resource.ID(),
				}

				err = build.UseInputs([]db.BuildInput{input})
				Expect(err).ToNot(HaveOccurred())
			})

			It("doesn't change the check_order when saving a new build output", func() {
				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "1"},
						Space:   atc.Space("space"),
					},
					atc.SpaceVersion{
						Version: atc.Version{"version": "2"},
						Space:   atc.Space("space"),
					},
					atc.SpaceVersion{
						Version: atc.Version{"version": "3"},
						Space:   atc.Space("space"),
					},
				})

				job, found, err := dbPipeline.Job("some-job")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())

				build, err := job.CreateBuild()
				Expect(err).ToNot(HaveOccurred())

				err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "3"})
				Expect(err).ToNot(HaveOccurred())

				beforeVR, err := resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(beforeVR).ToNot(BeEmpty())

				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "4"},
						Space:   atc.Space("space"),
					},
					atc.SpaceVersion{
						Version: atc.Version{"version": "5"},
						Space:   atc.Space("space"),
					},
				})

				err = build.SaveOutput(resourceConfig, atc.SpaceVersion{
					Space:   atc.Space("space"),
					Version: atc.Version(beforeVR[0].Version()),
				}, "some-output-name", "some-resource")
				Expect(err).ToNot(HaveOccurred())

				versions, _, found, err := resource.Versions(db.Page{Limit: 10})
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(versions).To(HaveLen(5))
				Expect(versions[0].Version).To(Equal(atc.Version{"version": "5"}))
				Expect(versions[1].Version).To(Equal(atc.Version{"version": "4"}))
				Expect(versions[2].Version).To(Equal(atc.Version{"version": "3"}))
				Expect(versions[3].Version).To(Equal(atc.Version{"version": "2"}))
				Expect(versions[4].Version).To(Equal(atc.Version{"version": "1"}))
			})
		})

		Describe("saving versioned resources", func() {
			It("updates the latest versioned resource", func() {
				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "1"},
						Space:   atc.Space("space"),
					},
				})

				savedResource, _, err := dbPipeline.Resource("some-resource")
				Expect(err).ToNot(HaveOccurred())

				err = savedResource.SetResourceConfig(resourceConfig.ID())
				Expect(err).ToNot(HaveOccurred())

				err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "1"})
				Expect(err).ToNot(HaveOccurred())

				savedVR, err := resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(savedVR).ToNot(BeEmpty())
				Expect(savedVR[0].Version()).To(Equal(db.Version{"version": "1"}))

				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "2"},
						Space:   atc.Space("space"),
					},
					atc.SpaceVersion{
						Version: atc.Version{"version": "3"},
						Space:   atc.Space("space"),
					},
				})

				err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "3"})
				Expect(err).ToNot(HaveOccurred())

				savedVR, err = resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(savedVR[0].Version()).To(Equal(db.Version{"version": "3"}))
			})
		})

		It("initially has no pending build for a job", func() {
			job, found, err := dbPipeline.Job("some-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			pendingBuilds, err := job.GetPendingBuilds()
			Expect(err).ToNot(HaveOccurred())
			Expect(pendingBuilds).To(HaveLen(0))
		})
	})

	Describe("Disable and Enable Resource Versions", func() {
		var pipelineDB db.Pipeline
		var resource db.Resource
		var resourceConfig db.ResourceConfig

		BeforeEach(func() {
			pipelineConfig := atc.Config{
				Jobs: atc.JobConfigs{
					{
						Name: "a-job",
					},
				},
				Resources: atc.ResourceConfigs{
					{
						Name:   "some-resource",
						Type:   "some-type",
						Source: atc.Source{"some-source": "some-value"},
					},
				},
			}
			var err error
			pipelineDB, _, err = team.SavePipeline("some-pipeline", pipelineConfig, db.ConfigVersion(1), db.PipelineUnpaused)
			Expect(err).ToNot(HaveOccurred())

			var found bool
			resource, found, err = pipelineDB.Resource("some-resource")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			resourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some-source": "some-value"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			err = resource.SetResourceConfig(resourceConfig.ID())
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when a version is disabled", func() {
			It("omits the version from the versions DB", func() {
				aJob, found, err := pipelineDB.Job("a-job")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())

				build1, err := aJob.CreateBuild()
				Expect(err).ToNot(HaveOccurred())

				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "disabled"},
						Space:   atc.Space("space"),
					},
				})

				err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "disabled"})
				Expect(err).ToNot(HaveOccurred())

				disabledVersion, err := resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(disabledVersion).ToNot(BeEmpty())

				disabledInput := db.BuildInput{
					Name:       "disabled-input",
					Version:    atc.Version{"version": "disabled"},
					Space:      atc.Space("space"),
					ResourceID: resource.ID(),
				}

				err = build1.SaveOutput(resourceConfig, atc.SpaceVersion{
					Space:   atc.Space("space"),
					Version: atc.Version{"version": "disabled"},
				}, "some-output-name", "some-resource")
				Expect(err).ToNot(HaveOccurred())

				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "enabled"},
						Space:   atc.Space("space"),
					},
				})

				err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "enabled"})
				Expect(err).ToNot(HaveOccurred())

				enabledVersion, err := resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(enabledVersion).ToNot(BeEmpty())

				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "other-enabled"},
						Space:   atc.Space("space"),
					},
				})

				err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "other-enabled"})
				Expect(err).ToNot(HaveOccurred())

				otherEnabledVersion, err := resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(otherEnabledVersion).ToNot(BeEmpty())

				enabledInput := db.BuildInput{
					Name:       "enabled-input",
					Space:      atc.Space("space"),
					Version:    atc.Version{"version": "enabled"},
					ResourceID: resource.ID(),
				}
				err = build1.UseInputs([]db.BuildInput{disabledInput, enabledInput})
				Expect(err).ToNot(HaveOccurred())

				Expect(err).ToNot(HaveOccurred())

				err = build1.SaveOutput(resourceConfig, atc.SpaceVersion{
					Space:   atc.Space("space"),
					Version: atc.Version{"version": "other-enabled"},
				}, "some-output-name", "some-resource")
				Expect(err).ToNot(HaveOccurred())

				err = build1.Finish(db.BuildStatusSucceeded)
				Expect(err).ToNot(HaveOccurred())

				err = resource.DisableVersion(disabledVersion[0].ID())
				Expect(err).ToNot(HaveOccurred())

				err = resource.DisableVersion(enabledVersion[0].ID())
				Expect(err).ToNot(HaveOccurred())

				err = resource.EnableVersion(enabledVersion[0].ID())
				Expect(err).ToNot(HaveOccurred())

				versions, err := pipelineDB.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())

				aJob, found, err = pipelineDB.Job("a-job")
				Expect(found).To(BeTrue())
				Expect(err).ToNot(HaveOccurred())

				By("omitting it from the list of resource versions")
				Expect(versions.ResourceVersions).To(ConsistOf(
					algorithm.ResourceVersion{
						VersionID:  enabledVersion[0].ID(),
						ResourceID: resource.ID(),
						CheckOrder: enabledVersion[0].CheckOrder(),
					},
					algorithm.ResourceVersion{
						VersionID:  otherEnabledVersion[0].ID(),
						ResourceID: resource.ID(),
						CheckOrder: otherEnabledVersion[0].CheckOrder(),
					},
				))

				By("omitting it from build outputs")
				Expect(versions.BuildOutputs).To(ConsistOf(
					// explicit output
					algorithm.BuildOutput{
						ResourceVersion: algorithm.ResourceVersion{
							VersionID:  otherEnabledVersion[0].ID(),
							ResourceID: resource.ID(),
							CheckOrder: otherEnabledVersion[0].CheckOrder(),
						},
						JobID:   aJob.ID(),
						BuildID: build1.ID(),
					},
					// implicit output
					algorithm.BuildOutput{
						ResourceVersion: algorithm.ResourceVersion{
							VersionID:  enabledVersion[0].ID(),
							ResourceID: resource.ID(),
							CheckOrder: enabledVersion[0].CheckOrder(),
						},
						JobID:   aJob.ID(),
						BuildID: build1.ID(),
					},
				))

				By("omitting it from build inputs")
				Expect(versions.BuildInputs).To(ConsistOf(
					algorithm.BuildInput{
						ResourceVersion: algorithm.ResourceVersion{
							VersionID:  enabledVersion[0].ID(),
							ResourceID: resource.ID(),
							CheckOrder: enabledVersion[0].CheckOrder(),
						},
						JobID:     aJob.ID(),
						BuildID:   build1.ID(),
						InputName: "enabled-input",
					},
				))
			})
		})
	})

	Describe("Destroy", func() {
		var resourceConfig db.ResourceConfig

		It("removes the pipeline and all of its data", func() {
			By("populating resources table")
			resource, found, err := pipeline.Resource("some-resource")
			Expect(found).To(BeTrue())
			Expect(err).ToNot(HaveOccurred())

			resourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some": "source"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			err = resource.SetResourceConfig(resourceConfig.ID())
			Expect(err).ToNot(HaveOccurred())

			By("populating resource versions")
			saveVersions(resourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"key": "value"},
					Space:   atc.Space("space"),
				},
			})

			By("populating builds")
			job, found, err := pipeline.Job("job-name")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			build, err := job.CreateBuild()
			Expect(err).ToNot(HaveOccurred())

			By("populating build inputs")
			err = build.UseInputs([]db.BuildInput{
				db.BuildInput{
					Name:       "build-input",
					ResourceID: resource.ID(),
					Space:      atc.Space("space"),
					Version:    atc.Version{"key": "value"},
				},
			})
			Expect(err).ToNot(HaveOccurred())

			By("populating build outputs")
			err = build.SaveOutput(resourceConfig, atc.SpaceVersion{
				Space:   atc.Space("space"),
				Version: atc.Version{"key": "value"},
			}, "some-output-name", "some-resource")
			Expect(err).ToNot(HaveOccurred())

			By("populating build events")
			err = build.SaveEvent(event.StartTask{})
			Expect(err).ToNot(HaveOccurred())

			err = pipeline.Destroy()
			Expect(err).ToNot(HaveOccurred())

			found, err = pipeline.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeFalse())

			found, err = build.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeFalse())

			_, found, err = team.Pipeline(pipeline.Name())
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeFalse())
		})
	})

	Describe("GetPendingBuilds/GetAllPendingBuilds", func() {
		Context("when a build is created", func() {
			BeforeEach(func() {
				_, err := job.CreateBuild()
				Expect(err).ToNot(HaveOccurred())
			})

			It("returns the build", func() {
				pendingBuildsForJob, err := job.GetPendingBuilds()
				Expect(err).ToNot(HaveOccurred())
				Expect(pendingBuildsForJob).To(HaveLen(1))

				pendingBuilds, err := pipeline.GetAllPendingBuilds()
				Expect(err).ToNot(HaveOccurred())
				Expect(pendingBuilds).To(HaveLen(1))
				Expect(pendingBuilds["job-name"]).ToNot(BeNil())
			})
		})
	})

	Describe("VersionsDB caching", func() {
		var otherPipeline db.Pipeline
		BeforeEach(func() {
			otherPipelineConfig := atc.Config{
				Resources: atc.ResourceConfigs{
					{
						Name: "some-other-resource",
						Type: "some-type",
						Source: atc.Source{
							"some-source": "some-other-value",
						},
					},
				},
				Jobs: atc.JobConfigs{
					{
						Name: "some-job",
					},
				},
			}
			var err error
			otherPipeline, _, err = team.SavePipeline("other-pipeline-name", otherPipelineConfig, 0, db.PipelineUnpaused)
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when build outputs are added", func() {
			var build db.Build
			var savedVR []db.ResourceVersion
			var resourceConfig db.ResourceConfig
			var savedResource db.Resource

			BeforeEach(func() {
				var err error
				job, found, err := pipeline.Job("job-name")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())

				build, err = job.CreateBuild()
				Expect(err).ToNot(HaveOccurred())

				savedResource, _, err = pipeline.Resource("some-resource")
				Expect(err).ToNot(HaveOccurred())

				resourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some": "source"}, creds.VersionedResourceTypes{})
				Expect(err).ToNot(HaveOccurred())

				err = savedResource.SetResourceConfig(resourceConfig.ID())
				Expect(err).ToNot(HaveOccurred())

				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "1"},
						Space:   atc.Space("space"),
					},
				})

				err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "1"})
				Expect(err).ToNot(HaveOccurred())

				savedVR, err = resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(savedVR).ToNot(BeEmpty())
			})

			It("will cache VersionsDB if no change has occured", func() {
				err := build.SaveOutput(resourceConfig, atc.SpaceVersion{
					Space:   atc.Space("space"),
					Version: atc.Version(savedVR[0].Version()),
				}, "some-output-name", "some-resource")
				Expect(err).ToNot(HaveOccurred())

				versionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())

				cachedVersionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())
				Expect(versionsDB == cachedVersionsDB).To(BeTrue(), "Expected VersionsDB to be the same object")
			})

			It("will not cache VersionsDB if a build has completed", func() {
				versionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())

				err = build.Finish(db.BuildStatusSucceeded)
				Expect(err).ToNot(HaveOccurred())

				cachedVersionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())
				Expect(versionsDB != cachedVersionsDB).To(BeTrue(), "Expected VersionsDB to be different objects")
			})

			It("will not cache VersionsDB if a resource version is disabled or enabled", func() {
				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "1"},
						Space:   atc.Space("space"),
					},
				})

				versionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())

				err = resourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "1"})
				Expect(err).ToNot(HaveOccurred())

				rcv, err := resourceConfig.LatestVersions()
				Expect(err).ToNot(HaveOccurred())
				Expect(rcv).ToNot(BeEmpty())

				err = savedResource.DisableVersion(rcv[0].ID())
				Expect(err).ToNot(HaveOccurred())

				cachedVersionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())
				Expect(versionsDB != cachedVersionsDB).To(BeTrue(), "Expected VersionsDB to be different objects")

				err = savedResource.EnableVersion(rcv[0].ID())
				Expect(err).ToNot(HaveOccurred())

				cachedVersionsDB2, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())
				Expect(cachedVersionsDB != cachedVersionsDB2).To(BeTrue(), "Expected VersionsDB to be different objects")
			})

			Context("when the build outputs are added for a different pipeline", func() {
				It("does not invalidate the cache for the original pipeline", func() {
					job, found, err := otherPipeline.Job("some-job")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue())

					otherBuild, err := job.CreateBuild()
					Expect(err).ToNot(HaveOccurred())

					otherSavedResource, _, err := otherPipeline.Resource("some-other-resource")
					Expect(err).ToNot(HaveOccurred())

					otherResourceConfig, err := resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some-source": "some-other-value"}, creds.VersionedResourceTypes{})
					Expect(err).ToNot(HaveOccurred())

					err = otherSavedResource.SetResourceConfig(otherResourceConfig.ID())
					Expect(err).ToNot(HaveOccurred())

					saveVersions(otherResourceConfig, []atc.SpaceVersion{
						atc.SpaceVersion{
							Version: atc.Version{"version": "1"},
							Space:   atc.Space("space"),
						},
					})

					err = otherResourceConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "1"})
					Expect(err).ToNot(HaveOccurred())

					otherSavedVR, err := otherResourceConfig.LatestVersions()
					Expect(err).ToNot(HaveOccurred())
					Expect(otherSavedVR).ToNot(BeEmpty())

					versionsDB, err := pipeline.LoadVersionsDB()
					Expect(err).ToNot(HaveOccurred())

					err = otherBuild.SaveOutput(otherResourceConfig, atc.SpaceVersion{
						Space:   atc.Space("space"),
						Version: atc.Version(otherSavedVR[0].Version()),
					}, "some-output-name", "some-other-resource")
					Expect(err).ToNot(HaveOccurred())

					cachedVersionsDB, err := pipeline.LoadVersionsDB()
					Expect(err).ToNot(HaveOccurred())
					Expect(versionsDB == cachedVersionsDB).To(BeTrue(), "Expected VersionsDB to be the same object")
				})
			})
		})

		Context("when versioned resources are added", func() {
			var resourceConfig db.ResourceConfig
			var otherResourceConfig db.ResourceConfig
			var resource db.Resource

			BeforeEach(func() {
				var err error
				resource, _, err = pipeline.Resource("some-resource")
				Expect(err).ToNot(HaveOccurred())

				otherResource, _, err := pipeline.Resource("some-other-resource")
				Expect(err).ToNot(HaveOccurred())

				resourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some": "source"}, creds.VersionedResourceTypes{})
				Expect(err).ToNot(HaveOccurred())

				otherResourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some": "other-source"}, creds.VersionedResourceTypes{})
				Expect(err).ToNot(HaveOccurred())

				err = resource.SetResourceConfig(resourceConfig.ID())
				Expect(err).ToNot(HaveOccurred())

				err = otherResource.SetResourceConfig(otherResourceConfig.ID())
				Expect(err).ToNot(HaveOccurred())
			})

			It("will cache VersionsDB if no change has occured", func() {
				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "1"},
						Space:   atc.Space("space"),
					},
				})

				versionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())

				cachedVersionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())
				Expect(versionsDB == cachedVersionsDB).To(BeTrue(), "Expected VersionsDB to be the same object")
			})

			It("will not cache VersionsDB if a change occured", func() {
				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "1"},
						Space:   atc.Space("space"),
					},
				})

				versionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())

				saveVersions(otherResourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "1"},
						Space:   atc.Space("space"),
					},
				})

				cachedVersionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())
				Expect(versionsDB != cachedVersionsDB).To(BeTrue(), "Expected VersionsDB to be different objects")
			})

			It("will not cache versions whose check order is zero", func() {
				saveVersions(resourceConfig, []atc.SpaceVersion{
					atc.SpaceVersion{
						Version: atc.Version{"version": "2"},
						Space:   atc.Space("space"),
					},
				})

				By("creating a new version but not updating the check order yet")
				created, err := resourceConfig.SaveUncheckedVersion(atc.Space("space"), atc.Version{"version": "1"}, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(created).To(BeTrue())

				build, err := job.CreateBuild()
				Expect(err).ToNot(HaveOccurred())

				err = build.UseInputs([]db.BuildInput{{Name: "some-resource", Version: atc.Version{"version": "1"}, ResourceID: resource.ID()}})
				Expect(err).ToNot(HaveOccurred())

				err = build.SaveOutput(resourceConfig, atc.SpaceVersion{
					Space:   atc.Space("space"),
					Version: atc.Version{"version": "1"},
				}, "some-resource", "some-resource")
				Expect(err).ToNot(HaveOccurred())

				versionsDB, err := pipeline.LoadVersionsDB()
				Expect(err).ToNot(HaveOccurred())
				Expect(versionsDB.ResourceVersions).To(HaveLen(1))
				Expect(versionsDB.BuildInputs).To(HaveLen(0))
				Expect(versionsDB.BuildOutputs).To(HaveLen(0))
			})

			Context("when the versioned resources are added for a different pipeline", func() {
				It("does not invalidate the cache for the original pipeline", func() {
					saveVersions(resourceConfig, []atc.SpaceVersion{
						atc.SpaceVersion{
							Version: atc.Version{"version": "1"},
							Space:   atc.Space("space"),
						},
					})

					versionsDB, err := pipeline.LoadVersionsDB()
					Expect(err).ToNot(HaveOccurred())

					otherPipelineResource, _, err := otherPipeline.Resource("some-other-resource")
					Expect(err).ToNot(HaveOccurred())

					otherPipelineResourceConfig, err := resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some-source": "some-other-value"}, creds.VersionedResourceTypes{})
					Expect(err).ToNot(HaveOccurred())

					err = otherPipelineResource.SetResourceConfig(otherPipelineResourceConfig.ID())
					Expect(err).ToNot(HaveOccurred())

					saveVersions(otherPipelineResourceConfig, []atc.SpaceVersion{
						atc.SpaceVersion{
							Version: atc.Version{"version": "1"},
							Space:   atc.Space("space"),
						},
					})

					saveVersions(otherPipelineResourceConfig, []atc.SpaceVersion{{
						Space:   atc.Space("space"),
						Version: atc.Version{"version": "1"},
					}})

					cachedVersionsDB, err := pipeline.LoadVersionsDB()
					Expect(err).ToNot(HaveOccurred())
					Expect(versionsDB == cachedVersionsDB).To(BeTrue(), "Expected VersionsDB to be the same object")
				})
			})
		})
	})

	Describe("Dashboard", func() {
		It("returns a Dashboard object with a DashboardJob corresponding to each configured job", func() {
			job, found, err := pipeline.Job("job-name")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			err = job.UpdateFirstLoggedBuildID(57)
			Expect(err).ToNot(HaveOccurred())

			otherJob, found, err := pipeline.Job("some-other-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			aJob, found, err := pipeline.Job("a-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			sharedJob, found, err := pipeline.Job("shared-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			randomJob, found, err := pipeline.Job("random-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			otherSerialGroupJob, found, err := pipeline.Job("other-serial-group-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			differentSerialGroupJob, found, err := pipeline.Job("different-serial-group-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			By("returning jobs with no builds")
			actualDashboard, err := pipeline.Dashboard()
			Expect(err).ToNot(HaveOccurred())

			Expect(actualDashboard[0].Job.Name()).To(Equal(job.Name()))
			Expect(actualDashboard[1].Job.Name()).To(Equal(otherJob.Name()))
			Expect(actualDashboard[2].Job.Name()).To(Equal(aJob.Name()))
			Expect(actualDashboard[3].Job.Name()).To(Equal(sharedJob.Name()))
			Expect(actualDashboard[4].Job.Name()).To(Equal(randomJob.Name()))
			Expect(actualDashboard[5].Job.Name()).To(Equal(otherSerialGroupJob.Name()))
			Expect(actualDashboard[6].Job.Name()).To(Equal(differentSerialGroupJob.Name()))

			By("returning a job's most recent pending build if there are no running builds")
			job, found, err = pipeline.Job("job-name")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			firstJobBuild, err := job.CreateBuild()
			Expect(err).ToNot(HaveOccurred())

			actualDashboard, err = pipeline.Dashboard()
			Expect(err).ToNot(HaveOccurred())

			Expect(actualDashboard[0].Job.Name()).To(Equal(job.Name()))
			Expect(actualDashboard[0].NextBuild.ID()).To(Equal(firstJobBuild.ID()))

			By("returning a job's most recent started build")
			found, err = firstJobBuild.Start("engine", `{"meta":"data"}`, atc.Plan{})
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			found, err = firstJobBuild.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			actualDashboard, err = pipeline.Dashboard()
			Expect(err).ToNot(HaveOccurred())

			Expect(actualDashboard[0].Job.Name()).To(Equal(job.Name()))
			Expect(actualDashboard[0].NextBuild.ID()).To(Equal(firstJobBuild.ID()))
			Expect(actualDashboard[0].NextBuild.Status()).To(Equal(db.BuildStatusStarted))
			Expect(actualDashboard[0].NextBuild.Engine()).To(Equal("engine"))
			Expect(actualDashboard[0].NextBuild.EngineMetadata()).To(Equal(`{"meta":"data"}`))

			By("returning a job's most recent started build even if there is a newer pending build")
			job, found, err = pipeline.Job("job-name")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			secondJobBuild, err := job.CreateBuild()
			Expect(err).ToNot(HaveOccurred())

			actualDashboard, err = pipeline.Dashboard()
			Expect(err).ToNot(HaveOccurred())

			Expect(actualDashboard[0].Job.Name()).To(Equal(job.Name()))
			Expect(actualDashboard[0].NextBuild.ID()).To(Equal(firstJobBuild.ID()))

			By("returning a job's most recent finished build")
			err = firstJobBuild.Finish(db.BuildStatusSucceeded)
			Expect(err).ToNot(HaveOccurred())

			err = secondJobBuild.Finish(db.BuildStatusSucceeded)
			Expect(err).ToNot(HaveOccurred())

			found, err = secondJobBuild.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			actualDashboard, err = pipeline.Dashboard()
			Expect(err).ToNot(HaveOccurred())

			Expect(actualDashboard[0].Job.Name()).To(Equal(job.Name()))
			Expect(actualDashboard[0].NextBuild).To(BeNil())
			Expect(actualDashboard[0].FinishedBuild.ID()).To(Equal(secondJobBuild.ID()))
		})
	})

	Describe("DeleteBuildEventsByBuildIDs", func() {
		It("deletes all build logs corresponding to the given build ids", func() {
			build1DB, err := team.CreateOneOffBuild()
			Expect(err).ToNot(HaveOccurred())

			err = build1DB.SaveEvent(event.Log{
				Payload: "log 1",
			})
			Expect(err).ToNot(HaveOccurred())

			build2DB, err := team.CreateOneOffBuild()
			Expect(err).ToNot(HaveOccurred())

			err = build2DB.SaveEvent(event.Log{
				Payload: "log 2",
			})
			Expect(err).ToNot(HaveOccurred())

			build3DB, err := team.CreateOneOffBuild()
			Expect(err).ToNot(HaveOccurred())

			err = build3DB.Finish(db.BuildStatusSucceeded)
			Expect(err).ToNot(HaveOccurred())

			err = build1DB.Finish(db.BuildStatusSucceeded)
			Expect(err).ToNot(HaveOccurred())

			err = build2DB.Finish(db.BuildStatusSucceeded)
			Expect(err).ToNot(HaveOccurred())

			build4DB, err := team.CreateOneOffBuild()
			Expect(err).ToNot(HaveOccurred())

			By("doing nothing if the list is empty")
			err = pipeline.DeleteBuildEventsByBuildIDs([]int{})
			Expect(err).ToNot(HaveOccurred())

			By("not returning an error")
			err = pipeline.DeleteBuildEventsByBuildIDs([]int{build3DB.ID(), build4DB.ID(), build1DB.ID()})
			Expect(err).ToNot(HaveOccurred())

			err = build4DB.Finish(db.BuildStatusSucceeded)
			Expect(err).ToNot(HaveOccurred())

			By("deleting events for build 1")
			events1, err := build1DB.Events(0)
			Expect(err).ToNot(HaveOccurred())
			defer db.Close(events1)

			_, err = events1.Next()
			Expect(err).To(Equal(db.ErrEndOfBuildEventStream))

			By("preserving events for build 2")
			events2, err := build2DB.Events(0)
			Expect(err).ToNot(HaveOccurred())
			defer db.Close(events2)

			build2Event1, err := events2.Next()
			Expect(err).ToNot(HaveOccurred())
			Expect(build2Event1).To(Equal(envelope(event.Log{
				Payload: "log 2",
			})))

			_, err = events2.Next() // finish event
			Expect(err).ToNot(HaveOccurred())

			_, err = events2.Next()
			Expect(err).To(Equal(db.ErrEndOfBuildEventStream))

			By("deleting events for build 3")
			events3, err := build3DB.Events(0)
			Expect(err).ToNot(HaveOccurred())
			defer db.Close(events3)

			_, err = events3.Next()
			Expect(err).To(Equal(db.ErrEndOfBuildEventStream))

			By("being unflapped by build 4, which had no events at the time")
			events4, err := build4DB.Events(0)
			Expect(err).ToNot(HaveOccurred())
			defer db.Close(events4)

			_, err = events4.Next() // finish event
			Expect(err).ToNot(HaveOccurred())

			_, err = events4.Next()
			Expect(err).To(Equal(db.ErrEndOfBuildEventStream))

			By("updating ReapTime for the affected builds")
			found, err := build1DB.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			Expect(build1DB.ReapTime()).To(BeTemporally(">", build1DB.EndTime()))

			found, err = build2DB.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			Expect(build2DB.ReapTime()).To(BeZero())

			found, err = build3DB.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			Expect(build3DB.ReapTime()).To(Equal(build1DB.ReapTime()))

			found, err = build4DB.Reload()
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			// Not required behavior, just a sanity check for what I think will happen
			Expect(build4DB.ReapTime()).To(Equal(build1DB.ReapTime()))
		})
	})

	Describe("Jobs", func() {
		var jobs []db.Job

		BeforeEach(func() {
			var err error
			jobs, err = pipeline.Jobs()
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns all the jobs", func() {
			Expect(jobs[0].Name()).To(Equal("job-name"))
			Expect(jobs[1].Name()).To(Equal("some-other-job"))
			Expect(jobs[2].Name()).To(Equal("a-job"))
			Expect(jobs[3].Name()).To(Equal("shared-job"))
			Expect(jobs[4].Name()).To(Equal("random-job"))
			Expect(jobs[5].Name()).To(Equal("other-serial-group-job"))
			Expect(jobs[6].Name()).To(Equal("different-serial-group-job"))
		})
	})

	Describe("GetBuildsWithVersionAsInput", func() {
		var (
			resourceConfigVersion int
			expectedBuilds        []db.Build
			resource              db.Resource
			dbSecondBuild         db.Build
			resourceConfig        db.ResourceConfig
		)

		BeforeEach(func() {
			job, found, err := pipeline.Job("job-name")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			build, err := job.CreateBuild()

			Expect(err).ToNot(HaveOccurred())
			expectedBuilds = append(expectedBuilds, build)

			secondBuild, err := job.CreateBuild()
			Expect(err).ToNot(HaveOccurred())
			expectedBuilds = append(expectedBuilds, secondBuild)

			someOtherJob, found, err := pipeline.Job("some-other-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			_, err = someOtherJob.CreateBuild()
			Expect(err).ToNot(HaveOccurred())

			dbBuild, found, err := buildFactory.Build(build.ID())
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			resource, _, err = pipeline.Resource("some-resource")
			Expect(err).ToNot(HaveOccurred())

			resourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some": "source"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			err = resource.SetResourceConfig(resourceConfig.ID())
			Expect(err).ToNot(HaveOccurred())

			saveVersions(resourceConfig, []atc.SpaceVersion{{
				Space:   atc.Space("space"),
				Version: atc.Version{"version": "v1"},
			}})

			err = dbBuild.UseInputs([]db.BuildInput{
				db.BuildInput{
					Name: "some-input",
					Version: atc.Version{
						"version": "v1",
					},
					Space:           atc.Space("space"),
					ResourceID:      resource.ID(),
					FirstOccurrence: true,
				},
			})
			Expect(err).ToNot(HaveOccurred())

			dbSecondBuild, found, err = buildFactory.Build(secondBuild.ID())
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			inputs1 := db.BuildInput{
				Name: "some-input",
				Version: atc.Version{
					"version": "v1",
				},
				Space:           atc.Space("space"),
				ResourceID:      resource.ID(),
				FirstOccurrence: true,
			}

			saveVersions(resourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "v2"},
					Space:   atc.Space("space"),
				},
				atc.SpaceVersion{
					Version: atc.Version{"version": "v3"},
					Space:   atc.Space("space"),
				},
				atc.SpaceVersion{
					Version: atc.Version{"version": "v4"},
					Space:   atc.Space("space"),
				},
			})

			err = dbSecondBuild.UseInputs([]db.BuildInput{
				inputs1,
				db.BuildInput{
					Name: "some-input",
					Version: atc.Version{
						"version": "v3",
					},
					Space:           atc.Space("space"),
					ResourceID:      resource.ID(),
					FirstOccurrence: true,
				},
			})
			Expect(err).ToNot(HaveOccurred())

			rcv1, found, err := resourceConfig.FindVersion(atc.Space("space"), atc.Version{"version": "v1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			resourceConfigVersion = rcv1.ID()
		})

		It("returns the two builds for which the provided version id was an input", func() {
			builds, err := pipeline.GetBuildsWithVersionAsInput(resource.ID(), resourceConfigVersion)
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(ConsistOf(expectedBuilds))
		})

		It("returns the one build that uses the version as an input", func() {
			rcv3, found, err := resourceConfig.FindVersion(atc.Space("space"), atc.Version{"version": "v3"})
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			builds, err := pipeline.GetBuildsWithVersionAsInput(resource.ID(), rcv3.ID())
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(HaveLen(1))
			Expect(builds[0]).To(Equal(dbSecondBuild))
		})

		It("returns an empty slice of builds when the provided version id exists but is not used", func() {
			rcv4, found, err := resourceConfig.FindVersion(atc.Space("space"), atc.Version{"version": "v4"})
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			builds, err := pipeline.GetBuildsWithVersionAsInput(resource.ID(), rcv4.ID())
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(Equal([]db.Build{}))
		})

		It("returns an empty slice of builds when the provided version id doesn't exist", func() {
			builds, err := pipeline.GetBuildsWithVersionAsInput(resource.ID(), resourceConfigVersion+100)
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(Equal([]db.Build{}))
		})

		It("returns an empty slice of builds when the provided resource id doesn't exist", func() {
			builds, err := pipeline.GetBuildsWithVersionAsInput(10293912, resourceConfigVersion)
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(Equal([]db.Build{}))
		})
	})

	Describe("GetBuildsWithVersionAsOutput", func() {
		var (
			resourceConfigVersion int
			expectedBuilds        []db.Build
			resourceConfig        db.ResourceConfig
			resource              db.Resource
			secondBuild           db.Build
		)

		BeforeEach(func() {
			job, found, err := pipeline.Job("job-name")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			build, err := job.CreateBuild()
			Expect(err).ToNot(HaveOccurred())
			expectedBuilds = append(expectedBuilds, build)

			secondBuild, err = job.CreateBuild()
			Expect(err).ToNot(HaveOccurred())
			expectedBuilds = append(expectedBuilds, secondBuild)

			someOtherJob, found, err := pipeline.Job("some-other-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			_, err = someOtherJob.CreateBuild()
			Expect(err).ToNot(HaveOccurred())

			dbBuild, found, err := buildFactory.Build(build.ID())
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			resource, _, err = pipeline.Resource("some-resource")
			Expect(err).ToNot(HaveOccurred())

			resourceConfig, err = resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some": "source"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			err = resource.SetResourceConfig(resourceConfig.ID())
			Expect(err).ToNot(HaveOccurred())

			saveVersions(resourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "v1"},
					Space:   atc.Space("space"),
					Metadata: atc.Metadata{
						atc.MetadataField{
							Name:  "some",
							Value: "value",
						},
					},
				},
				atc.SpaceVersion{
					Version: atc.Version{"version": "v3"},
					Space:   atc.Space("space"),
				},
				atc.SpaceVersion{
					Version: atc.Version{"version": "v4"},
					Space:   atc.Space("space"),
				},
			})

			err = dbBuild.SaveOutput(resourceConfig, atc.SpaceVersion{
				Space:   atc.Space("space"),
				Version: atc.Version{"version": "v1"},
			}, "some-output-name", "some-resource")
			Expect(err).ToNot(HaveOccurred())

			dbSecondBuild, found, err := buildFactory.Build(secondBuild.ID())
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			err = dbSecondBuild.SaveOutput(resourceConfig, atc.SpaceVersion{
				Space:   atc.Space("space"),
				Version: atc.Version{"version": "v1"},
			}, "some-output-name", "some-resource")
			Expect(err).ToNot(HaveOccurred())

			err = dbSecondBuild.SaveOutput(resourceConfig, atc.SpaceVersion{
				Space:   atc.Space("space"),
				Version: atc.Version{"version": "v3"},
			}, "some-output-name", "some-resource")
			Expect(err).ToNot(HaveOccurred())

			rcv1, found, err := resourceConfig.FindVersion(atc.Space("space"), atc.Version{"version": "v1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			resourceConfigVersion = rcv1.ID()
		})

		It("returns the two builds for which the provided version id was an output", func() {
			builds, err := pipeline.GetBuildsWithVersionAsOutput(resource.ID(), resourceConfigVersion)
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(ConsistOf(expectedBuilds))
		})

		It("returns the one build that uses the version as an input", func() {
			rcv3, found, err := resourceConfig.FindVersion(atc.Space("space"), atc.Version{"version": "v3"})
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			builds, err := pipeline.GetBuildsWithVersionAsOutput(resource.ID(), rcv3.ID())
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(HaveLen(1))
			Expect(builds[0].ID()).To(Equal(secondBuild.ID()))
		})

		It("returns an empty slice of builds when the provided version id exists but is not used", func() {
			rcv4, found, err := resourceConfig.FindVersion(atc.Space("space"), atc.Version{"version": "v4"})
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			builds, err := pipeline.GetBuildsWithVersionAsOutput(resource.ID(), rcv4.ID())
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(Equal([]db.Build{}))
		})

		It("returns an empty slice of builds when the provided resource id doesn't exist", func() {
			builds, err := pipeline.GetBuildsWithVersionAsOutput(10293912, resourceConfigVersion)
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(Equal([]db.Build{}))
		})

		It("returns an empty slice of builds when the provided version id doesn't exist", func() {
			builds, err := pipeline.GetBuildsWithVersionAsOutput(resource.ID(), resourceConfigVersion+100)
			Expect(err).ToNot(HaveOccurred())
			Expect(builds).To(Equal([]db.Build{}))
		})
	})

	Describe("Builds", func() {
		var expectedBuilds []db.Build

		BeforeEach(func() {
			_, err := team.CreateOneOffBuild()
			Expect(err).NotTo(HaveOccurred())

			job, found, err := pipeline.Job("job-name")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			build, err := job.CreateBuild()
			Expect(err).ToNot(HaveOccurred())
			expectedBuilds = append(expectedBuilds, build)

			secondBuild, err := job.CreateBuild()
			Expect(err).ToNot(HaveOccurred())
			expectedBuilds = append(expectedBuilds, secondBuild)

			someOtherJob, found, err := pipeline.Job("some-other-job")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			thirdBuild, err := someOtherJob.CreateBuild()
			Expect(err).ToNot(HaveOccurred())
			expectedBuilds = append(expectedBuilds, thirdBuild)
		})

		It("returns builds for the current pipeline", func() {
			builds, _, err := pipeline.Builds(db.Page{Limit: 10})
			Expect(err).NotTo(HaveOccurred())
			Expect(builds).To(ConsistOf(expectedBuilds))
		})
	})

	Describe("ResourceTypes", func() {
		var resourceTypes db.ResourceTypes

		BeforeEach(func() {
			var err error
			resourceType, _, err := pipeline.ResourceType("some-resource-type")
			Expect(err).ToNot(HaveOccurred())
			Expect(resourceType.Version()).To(BeNil())

			otherResourceType, _, err := pipeline.ResourceType("some-other-resource-type")
			Expect(err).ToNot(HaveOccurred())
			Expect(resourceType.Version()).To(BeNil())

			setupTx, err := dbConn.Begin()
			Expect(err).ToNot(HaveOccurred())

			brt := db.BaseResourceType{
				Name: "base-type",
			}
			_, err = brt.FindOrCreate(setupTx)
			Expect(err).NotTo(HaveOccurred())
			Expect(setupTx.Commit()).To(Succeed())

			resourceTypeConfig, err := resourceConfigFactory.FindOrCreateResourceConfig(logger, "base-type", atc.Source{"some": "type-source"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			err = resourceType.SetResourceConfig(resourceTypeConfig.ID())
			Expect(err).ToNot(HaveOccurred())

			err = resourceTypeConfig.SaveDefaultSpace(atc.Space("space"))
			Expect(err).ToNot(HaveOccurred())

			saveVersions(resourceTypeConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "1"},
					Space:   atc.Space("space"),
				},
				atc.SpaceVersion{
					Version: atc.Version{"version": "2"},
					Space:   atc.Space("space"),
				},
			})

			err = resourceTypeConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "2"})
			Expect(err).ToNot(HaveOccurred())

			otherResourceTypeConfig, err := resourceConfigFactory.FindOrCreateResourceConfig(logger, "base-type", atc.Source{"some": "other-type-source"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			err = otherResourceTypeConfig.SaveDefaultSpace(atc.Space("space"))
			Expect(err).ToNot(HaveOccurred())

			err = otherResourceType.SetResourceConfig(otherResourceTypeConfig.ID())
			Expect(err).ToNot(HaveOccurred())

			saveVersions(otherResourceTypeConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "3"},
					Space:   atc.Space("space"),
				},
			})

			saveVersions(otherResourceTypeConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: atc.Version{"version": "3"},
					Space:   atc.Space("space"),
				},
				atc.SpaceVersion{
					Version: atc.Version{"version": "5"},
					Space:   atc.Space("space"),
				},
			})

			err = otherResourceTypeConfig.SaveSpaceLatestVersion(atc.Space("space"), atc.Version{"version": "5"})
			Expect(err).ToNot(HaveOccurred())
		})

		JustBeforeEach(func() {
			var err error
			resourceTypes, err = pipeline.ResourceTypes()
			Expect(err).ToNot(HaveOccurred())
		})

		It("returns the version", func() {
			version1, err := resourceTypes[0].Version()
			Expect(err).ToNot(HaveOccurred())

			version2, err := resourceTypes[1].Version()
			Expect(err).ToNot(HaveOccurred())

			resourceTypeVersions := []atc.Version{version1, version2}
			Expect(resourceTypeVersions).To(ConsistOf(atc.Version{"version": "2"}, atc.Version{"version": "5"}))
		})
	})

	Describe("ResourceVersion", func() {
		var (
			resourceVersion, rv   atc.ResourceVersion
			resourceConfigVersion db.ResourceVersion
			resource              db.Resource
		)

		BeforeEach(func() {
			var found bool
			var err error
			resource, found, err = pipeline.Resource("some-resource")
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			resourceConfig, err := resourceConfigFactory.FindOrCreateResourceConfig(logger, "some-type", atc.Source{"some": "source"}, creds.VersionedResourceTypes{})
			Expect(err).ToNot(HaveOccurred())

			err = resource.SetResourceConfig(resourceConfig.ID())
			Expect(err).ToNot(HaveOccurred())

			version := atc.Version{"version": "1"}
			saveVersions(resourceConfig, []atc.SpaceVersion{
				atc.SpaceVersion{
					Version: version,
					Space:   atc.Space("space"),
				},
			})

			resourceConfigVersion, found, err = resourceConfig.FindVersion(atc.Space("space"), version)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())

			resourceVersion = atc.ResourceVersion{
				Version: version,
				ID:      resourceConfigVersion.ID(),
				Enabled: true,
			}
		})

		JustBeforeEach(func() {
			var found bool
			var err error

			rv, found, err = pipeline.ResourceVersion(resourceConfigVersion.ID())
			Expect(err).ToNot(HaveOccurred())
			Expect(found).To(BeTrue())
		})

		Context("when a resource is enabled", func() {
			It("should return the version with enabled set to true", func() {
				Expect(rv).To(Equal(resourceVersion))
			})
		})

		Context("when a resource is not enabled", func() {
			BeforeEach(func() {
				err := resource.DisableVersion(resourceConfigVersion.ID())
				Expect(err).ToNot(HaveOccurred())

				resourceVersion.Enabled = false
			})

			It("should return the version with enabled set to false", func() {
				Expect(rv).To(Equal(resourceVersion))
			})
		})
	})
})
