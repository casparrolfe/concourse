package exec_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/ioutil"

	"code.cloudfoundry.org/lager/lagertest"
	"github.com/cloudfoundry/bosh-cli/director/template"
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/creds"
	"github.com/concourse/concourse/atc/creds/credsfakes"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/db/dbfakes"
	"github.com/concourse/concourse/atc/exec"
	"github.com/concourse/concourse/atc/exec/execfakes"
	"github.com/concourse/concourse/atc/resource"
	"github.com/concourse/concourse/atc/resource/resourcefakes"
	"github.com/concourse/concourse/atc/worker"
	"github.com/concourse/concourse/atc/worker/workerfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("GetStep", func() {
	var (
		ctx    context.Context
		cancel func()

		fakeWorkerClient          *workerfakes.FakeClient
		fakeResourceFetcher       *resourcefakes.FakeFetcher
		fakeResourceCacheFactory  *dbfakes.FakeResourceCacheFactory
		fakeResourceConfigFactory *dbfakes.FakeResourceConfigFactory
		fakeResourceCache         *dbfakes.FakeUsedResourceCache
		fakeResourceConfig        *dbfakes.FakeResourceConfig
		fakeResourceVersion       *dbfakes.FakeResourceVersion
		fakeVariablesFactory      *credsfakes.FakeVariablesFactory
		variables                 creds.Variables
		fakeBuild                 *dbfakes.FakeBuild
		fakeDelegate              *execfakes.FakeGetDelegate
		getPlan                   *atc.GetPlan

		fakeVolume    *workerfakes.FakeVolume
		resourceTypes atc.VersionedResourceTypes

		artifactRepository *worker.ArtifactRepository
		state              *execfakes.FakeRunState

		factory exec.Factory
		getStep exec.Step
		stepErr error

		containerMetadata = db.ContainerMetadata{
			PipelineID: 4567,
			Type:       db.ContainerTypeGet,
			StepName:   "some-step",
		}

		stepMetadata testMetadata = []string{"a=1", "b=2"}

		teamID  = 123
		buildID = 42
		planID  = 56
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())

		fakeResourceFetcher = new(resourcefakes.FakeFetcher)
		fakeWorkerClient = new(workerfakes.FakeClient)
		fakeResourceCacheFactory = new(dbfakes.FakeResourceCacheFactory)
		fakeResourceCache = new(dbfakes.FakeUsedResourceCache)
		fakeResourceConfig = new(dbfakes.FakeResourceConfig)
		fakeResourceVersion = new(dbfakes.FakeResourceVersion)

		fakeVariablesFactory = new(credsfakes.FakeVariablesFactory)
		variables = template.StaticVariables{
			"source-param": "super-secret-source",
		}
		fakeVariablesFactory.NewVariablesReturns(variables)

		artifactRepository = worker.NewArtifactRepository()
		state = new(execfakes.FakeRunState)
		state.ArtifactsReturns(artifactRepository)

		fakeVolume = new(workerfakes.FakeVolume)
		fakeResourceFetcher.FetchReturns(fakeVolume, nil)

		fakeResourceFactory := new(resourcefakes.FakeResourceFactory)
		fakeResourceCache.ResourceConfigReturns(fakeResourceConfig)
		fakeResourceCacheFactory.FindOrCreateResourceCacheReturns(fakeResourceCache, nil)

		fakeBuild = new(dbfakes.FakeBuild)
		fakeBuild.IDReturns(buildID)
		fakeBuild.TeamIDReturns(teamID)

		resourceTypes = atc.VersionedResourceTypes{
			{
				ResourceType: atc.ResourceType{
					Name:   "custom-resource",
					Type:   "custom-type",
					Source: atc.Source{"some-custom": "source"},
				},
				Version: atc.Version{"some-custom": "version"},
			},
		}

		getPlan = &atc.GetPlan{
			Name:                   "some-name",
			Type:                   "some-resource-type",
			Source:                 atc.Source{"some": "((source-param))"},
			Params:                 atc.Params{"some-param": "some-value"},
			Tags:                   []string{"some", "tags"},
			Space:                  atc.Space("space"),
			Version:                &atc.Version{"some-version": "some-value"},
			VersionedResourceTypes: resourceTypes,
		}

		factory = exec.NewGardenFactory(fakeWorkerClient, fakeResourceFetcher, fakeResourceFactory, fakeResourceCacheFactory, fakeResourceConfigFactory, fakeVariablesFactory, atc.ContainerLimits{})

		fakeDelegate = new(execfakes.FakeGetDelegate)
	})

	AfterEach(func() {
		cancel()
	})

	JustBeforeEach(func() {
		getStep = factory.Get(
			lagertest.NewTestLogger("get-action-test"),
			atc.Plan{
				ID:  atc.PlanID(planID),
				Get: getPlan,
			},
			fakeBuild,
			stepMetadata,
			containerMetadata,
			fakeDelegate,
		)

		stepErr = getStep.Run(ctx, state)
	})

	It("initializes the resource with the correct type and session id, making sure that it is not ephemeral", func() {
		Expect(stepErr).ToNot(HaveOccurred())

		Expect(fakeResourceFetcher.FetchCallCount()).To(Equal(1))
		fctx, _, sid, tags, actualTeamID, actualResourceTypes, resourceInstance, sm, delegate := fakeResourceFetcher.FetchArgsForCall(0)
		Expect(fctx).To(Equal(ctx))
		Expect(sm).To(Equal(stepMetadata))
		Expect(sid).To(Equal(resource.Session{
			Metadata: db.ContainerMetadata{
				PipelineID:       4567,
				Type:             db.ContainerTypeGet,
				StepName:         "some-step",
				WorkingDirectory: "/tmp/build/get",
			},
		}))
		Expect(tags).To(ConsistOf("some", "tags"))
		Expect(actualTeamID).To(Equal(teamID))
		Expect(resourceInstance).To(Equal(resource.NewResourceInstance(
			"some-resource-type",
			atc.Space("space"),
			atc.Version{"some-version": "some-value"},
			atc.Source{"some": "super-secret-source"},
			atc.Params{"some-param": "some-value"},
			creds.NewVersionedResourceTypes(variables, resourceTypes),
			fakeResourceCache,
			db.NewBuildStepContainerOwner(buildID, atc.PlanID(planID), teamID),
		)))
		Expect(actualResourceTypes).To(Equal(creds.NewVersionedResourceTypes(variables, resourceTypes)))
		Expect(delegate).To(Equal(fakeDelegate))
		expectedLockName := fmt.Sprintf("%x",
			sha256.Sum256([]byte(
				`{"type":"some-resource-type","space":"space","version":{"some-version":"some-value"},"source":{"some":"super-secret-source"},"params":{"some-param":"some-value"},"worker_name":"fake-worker"}`,
			)),
		)

		Expect(resourceInstance.LockName("fake-worker")).To(Equal(expectedLockName))
	})

	Context("when fetching resource succeeds", func() {
		BeforeEach(func() {
			getPlan.Resource = "some-pipeline-resource"

			fakeResourceVersion.MetadataReturns(db.ResourceConfigMetadataFields{{Name: "some", Value: "metadata"}})
			fakeResourceConfig.FindUncheckedVersionReturns(fakeResourceVersion, true, nil)
			fakeResourceCache.ResourceConfigReturns(fakeResourceConfig)
			fakeResourceCacheFactory.FindOrCreateResourceCacheReturns(fakeResourceCache, nil)
		})

		It("returns nil", func() {
			Expect(stepErr).ToNot(HaveOccurred())
		})

		It("is successful", func() {
			Expect(getStep.Succeeded()).To(BeTrue())
		})

		It("finishes the step via the delegate", func() {
			Expect(fakeDelegate.FinishedCallCount()).To(Equal(1))
			_, status, info := fakeDelegate.FinishedArgsForCall(0)
			Expect(status).To(Equal(exec.ExitStatus(0)))
			Expect(info.Version).To(Equal(atc.Version{"some-version": "some-value"}))
			Expect(info.Metadata).To(Equal([]atc.MetadataField{{"some", "metadata"}}))
		})

		It("finds the resource config version", func() {
			Expect(fakeResourceConfig.FindUncheckedVersionCallCount()).To(Equal(1))

			space, version := fakeResourceConfig.FindUncheckedVersionArgsForCall(0)
			Expect(space).To(Equal(atc.Space("space")))
			Expect(version).To(Equal(atc.Version{"some-version": "some-value"}))
		})

		Context("when it fails to find the version", func() {
			disaster := errors.New("oops")

			BeforeEach(func() {
				fakeResourceConfig.FindUncheckedVersionReturns(nil, false, disaster)
			})

			It("returns an error", func() {
				Expect(stepErr).To(Equal(disaster))
			})
		})

		Context("when the version is not found", func() {
			BeforeEach(func() {
				fakeResourceConfig.FindUncheckedVersionReturns(nil, false, nil)
			})

			It("does not return an error", func() {
				Expect(stepErr).To(BeNil())
			})
		})

		Describe("the source registered with the repository", func() {
			var artifactSource worker.ArtifactSource

			JustBeforeEach(func() {
				var found bool
				artifactSource, found = artifactRepository.SourceFor("some-name")
				Expect(found).To(BeTrue())
			})

			Describe("streaming to a destination", func() {
				var fakeDestination *workerfakes.FakeArtifactDestination

				BeforeEach(func() {
					fakeDestination = new(workerfakes.FakeArtifactDestination)
				})

				Context("when the resource can stream out", func() {
					var (
						streamedOut io.ReadCloser
					)

					BeforeEach(func() {
						streamedOut = gbytes.NewBuffer()
						fakeVolume.StreamOutReturns(streamedOut, nil)
					})

					It("streams the resource to the destination", func() {
						err := artifactSource.StreamTo(fakeDestination)
						Expect(err).NotTo(HaveOccurred())

						Expect(fakeVolume.StreamOutCallCount()).To(Equal(1))
						Expect(fakeVolume.StreamOutArgsForCall(0)).To(Equal("."))

						Expect(fakeDestination.StreamInCallCount()).To(Equal(1))
						dest, src := fakeDestination.StreamInArgsForCall(0)
						Expect(dest).To(Equal("."))
						Expect(src).To(Equal(streamedOut))
					})

					Context("when streaming out of the versioned source fails", func() {
						disaster := errors.New("nope")

						BeforeEach(func() {
							fakeVolume.StreamOutReturns(nil, disaster)
						})

						It("returns the error", func() {
							Expect(artifactSource.StreamTo(fakeDestination)).To(Equal(disaster))
						})
					})

					Context("when streaming in to the destination fails", func() {
						disaster := errors.New("nope")

						BeforeEach(func() {
							fakeDestination.StreamInReturns(disaster)
						})

						It("returns the error", func() {
							Expect(artifactSource.StreamTo(fakeDestination)).To(Equal(disaster))
						})
					})
				})

				Context("when the resource cannot stream out", func() {
					disaster := errors.New("nope")

					BeforeEach(func() {
						fakeVolume.StreamOutReturns(nil, disaster)
					})

					It("returns the error", func() {
						Expect(artifactSource.StreamTo(fakeDestination)).To(Equal(disaster))
					})
				})
			})

			Describe("streaming a file out", func() {
				Context("when the resource can stream out", func() {
					var (
						fileContent = "file-content"

						tgzBuffer *gbytes.Buffer
					)

					BeforeEach(func() {
						tgzBuffer = gbytes.NewBuffer()
						fakeVolume.StreamOutReturns(tgzBuffer, nil)
					})

					Context("when the file exists", func() {
						BeforeEach(func() {
							gzWriter := gzip.NewWriter(tgzBuffer)
							defer gzWriter.Close()

							tarWriter := tar.NewWriter(gzWriter)
							defer tarWriter.Close()

							err := tarWriter.WriteHeader(&tar.Header{
								Name: "some-file",
								Mode: 0644,
								Size: int64(len(fileContent)),
							})
							Expect(err).NotTo(HaveOccurred())

							_, err = tarWriter.Write([]byte(fileContent))
							Expect(err).NotTo(HaveOccurred())
						})

						It("streams out the given path", func() {
							reader, err := artifactSource.StreamFile("some-path")
							Expect(err).NotTo(HaveOccurred())

							Expect(ioutil.ReadAll(reader)).To(Equal([]byte(fileContent)))

							Expect(fakeVolume.StreamOutArgsForCall(0)).To(Equal("some-path"))
						})

						Describe("closing the stream", func() {
							It("closes the stream from the versioned source", func() {
								reader, err := artifactSource.StreamFile("some-path")
								Expect(err).NotTo(HaveOccurred())

								Expect(tgzBuffer.Closed()).To(BeFalse())

								err = reader.Close()
								Expect(err).NotTo(HaveOccurred())

								Expect(tgzBuffer.Closed()).To(BeTrue())
							})
						})
					})

					Context("but the stream is empty", func() {
						It("returns ErrFileNotFound", func() {
							_, err := artifactSource.StreamFile("some-path")
							Expect(err).To(MatchError(exec.FileNotFoundError{Path: "some-path"}))
						})
					})
				})

				Context("when the resource cannot stream out", func() {
					disaster := errors.New("nope")

					BeforeEach(func() {
						fakeVolume.StreamOutReturns(nil, disaster)
					})

					It("returns the error", func() {
						_, err := artifactSource.StreamFile("some-path")
						Expect(err).To(Equal(disaster))
					})
				})
			})
		})
	})

	Context("when fetching the resource exits unsuccessfully", func() {
		BeforeEach(func() {
			fakeResourceFetcher.FetchReturns(nil, atc.ErrResourceScriptFailed{
				ExitStatus: 42,
			})
		})

		It("finishes the step via the delegate", func() {
			Expect(fakeDelegate.FinishedCallCount()).To(Equal(1))
			_, status, info := fakeDelegate.FinishedArgsForCall(0)
			Expect(status).To(Equal(exec.ExitStatus(42)))
			Expect(info).To(BeZero())
		})

		It("returns nil", func() {
			Expect(stepErr).ToNot(HaveOccurred())
		})

		It("is not successful", func() {
			Expect(getStep.Succeeded()).To(BeFalse())
		})
	})

	Context("when fetching the resource errors", func() {
		disaster := errors.New("oh no")

		BeforeEach(func() {
			fakeResourceFetcher.FetchReturns(nil, disaster)
		})

		It("does not finish the step via the delegate", func() {
			Expect(fakeDelegate.FinishedCallCount()).To(Equal(0))
		})

		It("returns the error", func() {
			Expect(stepErr).To(Equal(disaster))
		})

		It("is not successful", func() {
			Expect(getStep.Succeeded()).To(BeFalse())
		})
	})
})
