package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
)

type fakeJobs struct {
	job          Job
	advanced     string
	retried      string
	advanceError error
}

func (f *fakeJobs) Claim(context.Context) (*Job, error) { return &f.job, nil }
func (f *fakeJobs) Advance(_ context.Context, _ *Job, next string) error {
	f.advanced = next
	return f.advanceError
}
func (f *fakeJobs) Retry(_ context.Context, _ *Job, code string) error { f.retried = code; return nil }
func (f *fakeJobs) Block(_ context.Context, _ *Job, code string) error {
	f.retried = "blocked:" + code
	return nil
}

type fakeEnterprise struct {
	failure bool
	calls   []string
}

func (f *fakeEnterprise) CheckTransfer(context.Context, tenancy.Identity) error {
	if f.failure {
		return &tenancy.OperationRejected{Code: "GROUP_OWNERSHIP_TRANSFER_REQUIRED"}
	}
	return nil
}

func (f *fakeEnterprise) PrepareIdentity(context.Context, string, tenancy.Identity, ProvisionInput) error {
	f.calls = append(f.calls, "prepare")
	if f.failure {
		return errors.New("network failure with sensitive response")
	}
	return nil
}
func (f *fakeEnterprise) RevokeIdentity(context.Context, string, tenancy.Identity) error {
	f.calls = append(f.calls, "revoke")
	if f.failure {
		return errors.New("unconfirmed IM revoke")
	}
	return nil
}

func TestTransferNeverActivatesBeforeConfirmedSourceRevoke(t *testing.T) {
	for _, step := range []string{"revoke_source", "prepare_target"} {
		jobs := &fakeJobs{job: Job{ID: "job", Step: step}}
		peer := &fakeEnterprise{failure: true}
		if _, err := (Worker{Store: jobs, Enterprise: peer}).Once(t.Context()); err != nil {
			t.Fatal(err)
		}
		if jobs.advanced != "" || jobs.retried != "ENTERPRISE_OPERATION_UNCONFIRMED" {
			t.Fatal(jobs)
		}
	}
}
func TestTaskProgression(t *testing.T) {
	for step, next := range map[string]string{"revoke_source": "prepare_target", "prepare_target": "activate", "activate": "completed"} {
		jobs := &fakeJobs{job: Job{Step: step}}
		peer := &fakeEnterprise{}
		if _, err := (Worker{Store: jobs, Enterprise: peer}).Once(t.Context()); err != nil || jobs.advanced != next {
			t.Fatalf("%s -> %s: %v", step, jobs.advanced, err)
		}
	}
	for _, pair := range [][2]string{{"revoke_source", "completed"}, {"prepare_target", "completed"}, {"completed", "activate"}} {
		if validTransition(pair[0], pair[1]) {
			t.Fatal("skipped safety fence", pair)
		}
	}
}

func TestTaskProgressFailureKeepsOriginalTaskRetryable(t *testing.T) {
	jobs := &fakeJobs{job: Job{ID: "original-job", Step: "activate"}, advanceError: ErrDenied}
	if worked, err := (Worker{Store: jobs, Enterprise: &fakeEnterprise{}}).Once(t.Context()); !worked || err != nil {
		t.Fatal(worked, err)
	}
	if jobs.job.ID != "original-job" || jobs.job.Step != "activate" || jobs.retried != "IDENTITY_PROGRESS_UNCONFIRMED" {
		t.Fatal("failed progress not retained for retry")
	}
}
func TestPhoneCanonicalization(t *testing.T) {
	for _, input := range []string{"13812345678", " +8613812345678 "} {
		phone, err := NormalizePhone(input)
		if err != nil || phone != "13812345678" {
			t.Fatal(phone, err)
		}
	}
	for _, input := range []string{"1381234567", "138123456789", "1381234567a", "１３８１２３４５６７８"} {
		if _, err := NormalizePhone(input); err == nil {
			t.Fatal(input)
		}
	}
}
