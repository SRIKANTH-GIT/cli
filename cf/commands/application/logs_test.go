package application_test

import (
	"time"

	"code.cloudfoundry.org/cli/cf/api/logs"
	"code.cloudfoundry.org/cli/cf/api/logs/logsfakes"
	"code.cloudfoundry.org/cli/cf/commandregistry"
	"code.cloudfoundry.org/cli/cf/errors"
	"code.cloudfoundry.org/cli/cf/models"
	"code.cloudfoundry.org/cli/cf/requirements"
	"code.cloudfoundry.org/cli/cf/requirements/requirementsfakes"
	testcmd "code.cloudfoundry.org/cli/utils/testhelpers/commands"
	testconfig "code.cloudfoundry.org/cli/utils/testhelpers/configuration"
	testlogs "code.cloudfoundry.org/cli/utils/testhelpers/logs"
	testterm "code.cloudfoundry.org/cli/utils/testhelpers/terminal"
	"github.com/cloudfoundry/loggregatorlib/logmessage"

	"code.cloudfoundry.org/cli/cf/configuration/coreconfig"
	. "code.cloudfoundry.org/cli/utils/testhelpers/matchers"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("logs command", func() {
	var (
		ui                  *testterm.FakeUI
		logsRepo            *logsfakes.FakeRepository
		requirementsFactory *requirementsfakes.FakeFactory
		configRepo          coreconfig.Repository
		deps                commandregistry.Dependency
	)

	updateCommandDependency := func(pluginCall bool) {
		deps.UI = ui
		deps.RepoLocator = deps.RepoLocator.SetLogsRepository(logsRepo)
		deps.Config = configRepo
		commandregistry.Commands.SetCommand(commandregistry.Commands.FindCommand("logs").SetDependency(deps, pluginCall))
	}

	BeforeEach(func() {
		ui = &testterm.FakeUI{}
		configRepo = testconfig.NewRepositoryWithDefaults()
		logsRepo = new(logsfakes.FakeRepository)
		requirementsFactory = new(requirementsfakes.FakeFactory)
	})

	runCommand := func(args ...string) bool {
		return testcmd.RunCLICommand("logs", args, requirementsFactory, updateCommandDependency, false, ui)
	}

	Describe("requirements", func() {
		It("fails with usage when called without one argument", func() {
			requirementsFactory.NewLoginRequirementReturns(requirements.Passing{})

			runCommand()
			Expect(ui.Outputs()).To(ContainSubstrings(
				[]string{"Incorrect Usage", "Requires an argument"},
			))
		})

		It("fails requirements when not logged in", func() {
			requirementsFactory.NewLoginRequirementReturns(requirements.Failing{})

			Expect(runCommand("my-app")).To(BeFalse())
		})

		It("fails if a space is not targeted", func() {
			requirementsFactory.NewLoginRequirementReturns(requirements.Passing{})
			requirementsFactory.NewTargetedSpaceRequirementReturns(requirements.Failing{Message: "not targeting space"})
			Expect(runCommand("--recent", "my-app")).To(BeFalse())
		})

	})

	Context("when logged in", func() {
		var (
			app models.Application
		)

		BeforeEach(func() {
			requirementsFactory.NewLoginRequirementReturns(requirements.Passing{})
			requirementsFactory.NewTargetedSpaceRequirementReturns(requirements.Passing{})

			app = models.Application{}
			app.Name = "my-app"
			app.GUID = "my-app-guid"

			currentTime := time.Now()
			recentLogs := []logs.Loggable{
				testlogs.NewLogMessage("Log Line 1", app.GUID, "DEA", "1", logmessage.LogMessage_ERR, currentTime),
				testlogs.NewLogMessage("Log Line 2", app.GUID, "DEA", "1", logmessage.LogMessage_ERR, currentTime),
			}

			appLogs := []logs.Loggable{
				testlogs.NewLogMessage("Log Line 1", app.GUID, "DEA", "1", logmessage.LogMessage_ERR, time.Now()),
			}

			applicationReq := new(requirementsfakes.FakeApplicationRequirement)
			applicationReq.GetApplicationReturns(app)
			requirementsFactory.NewApplicationRequirementReturns(applicationReq)

			logsRepo.RecentLogsForReturns(recentLogs, nil)
			logsRepo.TailLogsForStub = func(appGUID string, onConnect func(), logChan chan<- logs.Loggable, errChan chan<- error) {
				onConnect()
				go func() {
					for _, log := range appLogs {
						logChan <- log
					}
					close(logChan)
					close(errChan)
				}()
			}
		})

		It("shows the recent logs when the --recent flag is provided", func() {
			runCommand("--recent", "my-app")

			Expect(app.GUID).To(Equal(logsRepo.RecentLogsForArgsForCall(0)))
			Expect(ui.Outputs()).To(ContainSubstrings(
				[]string{"Connected, dumping recent logs for app", "my-app", "my-org", "my-space", "my-user"},
				[]string{"Log Line 1"},
				[]string{"Log Line 2"},
			))
		})

		Context("when the log messages contain format string identifiers", func() {
			BeforeEach(func() {
				logsRepo.RecentLogsForReturns([]logs.Loggable{
					testlogs.NewLogMessage("hello%2Bworld%v", app.GUID, "DEA", "1", logmessage.LogMessage_ERR, time.Now()),
				}, nil)
			})

			It("does not treat them as format strings", func() {
				runCommand("--recent", "my-app")
				Expect(ui.Outputs()).To(ContainSubstrings([]string{"hello%2Bworld%v"}))
			})
		})

		It("tails the app's logs when no flags are given", func() {
			runCommand("my-app")

			appGUID, _, _, _ := logsRepo.TailLogsForArgsForCall(0)
			Expect(app.GUID).To(Equal(appGUID))
			Expect(ui.Outputs()).To(ContainSubstrings(
				[]string{"Connected, tailing logs for app", "my-app", "my-org", "my-space", "my-user"},
				[]string{"Log Line 1"},
			))
		})

		Context("when the loggregator server has an invalid cert", func() {
			Context("when the skip-ssl-validation flag is not set", func() {
				It("fails and informs the user about the skip-ssl-validation flag", func() {
					logsRepo.TailLogsForStub = func(appGUID string, onConnect func(), logChan chan<- logs.Loggable, errChan chan<- error) {
						errChan <- errors.NewInvalidSSLCert("https://example.com", "it don't work good")
					}
					runCommand("my-app")

					Expect(ui.Outputs()).To(ContainSubstrings(
						[]string{"Received invalid SSL certificate", "https://example.com"},
						[]string{"TIP"},
					))
				})

				It("informs the user of the error when they include the --recent flag", func() {
					logsRepo.RecentLogsForReturns(nil, errors.NewInvalidSSLCert("https://example.com", "how does SSL work???"))
					runCommand("--recent", "my-app")

					Expect(ui.Outputs()).To(ContainSubstrings(
						[]string{"Received invalid SSL certificate", "https://example.com"},
						[]string{"TIP"},
					))
				})
			})
		})

		Context("when the loggregator server has a valid cert", func() {
			It("tails logs", func() {
				runCommand("my-app")
				Expect(ui.Outputs()).To(ContainSubstrings(
					[]string{"Connected, tailing logs for app", "my-org", "my-space", "my-user"},
				))
			})
		})
	})
})
