package organizer

import (
	"sync"
	"testing"
)

func TestOrgStatus(t *testing.T) {
	f := setup(t)
	const null = `{"status":null}`
	expect(t, f.do(t, "GET", "/api/org/status", nil, nil), 200, null)
	expect(t, f.do(t, "GET", "/api/org/status", nil, f.noOrg), 200, null)
	expect(t, f.do(t, "GET", "/api/org/status", nil, f.stale), 200, null)

	ok200(t, f.do(t, "GET", "/api/org/status", nil, f.member), "org-status.json")
	expect(t, f.do(t, "GET", "/api/org/status", nil, f.member), 200, `{"status":"active","statusNote":null,"allowResubmit":false}`)
	f.Exec(t, `update organization set status = 'weird', status_note = 'n', allow_resubmit = true where id = 'org1'`)
	expect(t, f.do(t, "GET", "/api/org/status", nil, f.member), 200, `{"status":"pending","statusNote":"n","allowResubmit":true}`)
}

func TestVerifyOrg(t *testing.T) {
	f := setup(t)
	body := map[string]any{"ktpNumber": "3201", "bankName": "BCA", "accountNumber": "0001", "accountHolderName": "A & B"}
	authCases(t, f, "PATCH", "/api/org/verify", body)
	expect(t, f.do(t, "PATCH", "/api/org/verify", body, f.member), 403, errBody("Forbidden"))

	expect(t, f.do(t, "PATCH", "/api/org/verify", body, f.admin), 409, errBody(msgCannotResubmit))
	f.Exec(t, `update organization set status = 'rejected' where id = 'org1'`)
	expect(t, f.do(t, "PATCH", "/api/org/verify", body, f.admin), 409, errBody(msgCannotResubmit))

	f.Exec(t, `update organization set allow_resubmit = true where id = 'org1'`)
	expect(t, f.do(t, "PATCH", "/api/org/verify", body, f.admin), 200, `{"ok":true}`)
	if got := f.scalar(t, `select status || '|' || allow_resubmit || '|' || verification_data from organization where id='org1'`); got !=
		`review|false|{"accountHolderName":"A & B","accountNumber":"0001","bankName":"BCA","ktpNumber":"3201"}` {
		t.Fatalf("org %v", got)
	}
	if got := f.scalar(t, `select coalesce(from_status,'-') || '>' || to_status || '|' || actor_id || '|' || actor_type || '|' || (note is null)::text
		from organization_status_history where organization_id='org1'`); got != "rejected>review|adm|organizer|true" {
		t.Fatalf("history %v", got)
	}
	expect(t, f.do(t, "PATCH", "/api/org/verify", body, f.owner), 200, `{"ok":true}`)
	if f.scalar(t, `select count(*) from organization_status_history`) != int64(1) {
		t.Fatal("no-op transition recorded")
	}
	f.Exec(t, `update organization set status = 'pending' where id = 'org2'`)
	expect(t, f.do(t, "PATCH", "/api/org/verify", map[string]any{"nib": "1"}, f.other), 200, `{"ok":true}`)
	if f.scalar(t, `select from_status || '>' || to_status from organization_status_history where organization_id='org2'`) != "pending>review" {
		t.Fatal("org2 history")
	}
}

func TestLogOrgCreationConcurrent(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = f.do(t, "POST", "/api/org/log-creation", map[string]any{"organizationId": "org1"}, f.owner).Code
		}()
	}
	wg.Wait()
	for _, c := range codes {
		if c != 200 {
			t.Fatalf("codes %v", codes)
		}
	}
	if n := f.scalar(t, `select count(*) from organization_status_history where organization_id = 'org1'`); n != int64(1) {
		t.Fatalf("rows %v", n)
	}
}

func TestLogOrgCreation(t *testing.T) {
	f := setup(t)
	expect(t, f.do(t, "POST", "/api/org/log-creation", map[string]any{"organizationId": "org1"}, nil), 401, errBody("Unauthorized"))
	expect(t, f.do(t, "POST", "/api/org/log-creation", map[string]any{}, f.owner), 400, errBody("Missing organizationId"))
	expect(t, f.do(t, "POST", "/api/org/log-creation", map[string]any{"organizationId": 5}, f.owner), 400, errBody("Missing organizationId"))
	expect(t, f.do(t, "POST", "/api/org/log-creation", map[string]any{"organizationId": "org1"}, f.admin), 403, errBody("Forbidden"))
	expect(t, f.do(t, "POST", "/api/org/log-creation", map[string]any{"organizationId": "org2"}, f.owner), 403, errBody("Forbidden"))
	ownerNoOrg := f.Login(t, "owner", nil)
	expect(t, f.do(t, "POST", "/api/org/log-creation", map[string]any{"organizationId": "org1"}, ownerNoOrg), 200, `{"ok":true}`)
	expect(t, f.do(t, "POST", "/api/org/log-creation", map[string]any{"organizationId": "org1"}, f.owner), 200, `{"ok":true}`)
	if got := f.scalar(t, `select count(*)::text || '|' || max(coalesce(from_status,'null') || '>' || to_status || '|' || actor_id || '|' || actor_type)
		from organization_status_history`); got != "1|null>pending|owner|organizer" {
		t.Fatalf("history %v", got)
	}
}
