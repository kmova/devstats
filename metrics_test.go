package gha2db

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
	"testing"
	"time"

	lib "k8s.io/test-infra/gha2db"
	testlib "k8s.io/test-infra/gha2db/test"
)

// MetricTestCase - used to test single metric
// setup is called to create database entries for metric to return results
// metric - metrics/{{metric}}.sql file is used to run metric, inside file {{from}} and {{to}} are replaced with from, to
// from, to - used as data range when calling metric
// expected - we're expecting this result from metric, it can either be a single row with single column numeric value
// or multiple rows, each containing metric name and its numeric value
type MetricTestCase struct {
	setup    func(*sql.DB, *lib.Ctx) error
	metric   string
	from     time.Time
	to       time.Time
	debugDB  bool // if set, test will not drop database at the end and will return after such test, so You can run metric manually via `runq` or directly on DB
	expected [][]interface{}
}

// Tests all metrics
func TestMetrics(t *testing.T) {
	// Test cases for each metric
	ft := testlib.YMDHMS

	// Please add new cases here
	// And their setup function at the bottom of this file
	var testCases = []MetricTestCase{
		{
			setup:    setupReviewersMetric,
			metric:   "reviewers",
			from:     ft(2017, 7, 9),
			to:       ft(2017, 7, 25),
			expected: [][]interface{}{{7}},
		},
		{
			setup:    setupReviewersMetric,
			metric:   "reviewers",
			from:     ft(2017, 6),
			to:       ft(2017, 7, 12, 23),
			debugDB:  false,
			expected: [][]interface{}{{3}},
		},
		{
			setup:  setupSigMentionsMetric,
			metric: "sig_mentions",
			from:   ft(2017, 7),
			to:     ft(2017, 8),
			expected: [][]interface{}{
				{"sig-group-1", 3},
				{"sig-group2", 3},
				{"sig-a-b-c", 1},
			},
		},
		{
			setup:  setupPRsMergedMetric,
			metric: "prs_merged",
			from:   ft(2017, 7),
			to:     ft(2017, 8),
			expected: [][]interface{}{
				{"Repo 1", 3},
				{"Repo 2", 2},
				{"Repo 3", 1},
			},
		},
		{
			setup:    setupPRsMergedMetric,
			metric:   "all_prs_merged",
			from:     ft(2017, 7),
			to:       ft(2017, 8),
			expected: [][]interface{}{{6}},
		},
		{
			setup:    setupOpenedToMergedMetric,
			metric:   "opened_to_merged",
			from:     ft(2017, 7),
			to:       ft(2017, 8),
			expected: [][]interface{}{{48}},
		},
	}

	// Environment context parse
	var ctx lib.Ctx
	ctx.Init()

	// Do not allow to run tests in "gha" database
	if ctx.PgDB == "gha" {
		t.Errorf("tests cannot be run on \"gha\" database")
		return
	}

	// Execute test cases
	for index, test := range testCases {
		got, err := executeMetricTestCase(&test, &ctx)
		if err != nil {
			t.Errorf("test number %d: %v", index+1, err.Error())
		}
		if !testlib.CompareSlices2D(test.expected, got) {
			t.Errorf("test number %d, expected %+v, got %+v", index+1, test.expected, got)
		}
		if test.debugDB {
			t.Errorf("returning due to debugDB mode")
			return
		}
	}
}

// This executes test of single metric
// All metric data is defined in "testMetric" argument
// Singel metric test is dropping & creating database from scratch (to avoid junky database)
// It also creates full DB structure - without indexes - they're not needed in
// small databases - like the ones created by test covergae tools
func executeMetricTestCase(testMetric *MetricTestCase, ctx *lib.Ctx) (result [][]interface{}, err error) {
	// Drop database if exists
	lib.DropDatabaseIfExists(ctx)

	// Create database if needed
	createdDatabase := lib.CreateDatabaseIfNeeded(ctx)
	if !createdDatabase {
		err = fmt.Errorf("failed to create database \"%s\"", ctx.PgDB)
		return
	}

	// Drop database after tests
	if !testMetric.debugDB {
		defer func() {
			// Drop database after tests
			lib.DropDatabaseIfExists(ctx)
		}()
	}

	// Connect to Postgres DB
	c := lib.PgConn(ctx)
	defer c.Close()

	// Create DB structure
	lib.Structure(ctx)

	// Execute metrics setup function
	err = testMetric.setup(c, ctx)
	if err != nil {
		return
	}

	// Execute metric and get its results
	result, err = executeMetric(c, ctx, testMetric.metric, testMetric.from, testMetric.to)

	// We're after succesfull setup
	return
}

// execute metric metrics/{{metric}}.sql with {{from}} and {{to}} replaced by from/YMDHMS, to/YMDHMS
// end result slice of slices of any type
func executeMetric(c *sql.DB, ctx *lib.Ctx, metric string, from, to time.Time) (result [][]interface{}, err error) {
	// Metric file name
	sqlFile := fmt.Sprintf("metrics/%s.sql", metric)

	// Read and transform SQL file.
	bytes, err := ioutil.ReadFile(sqlFile)
	if err != nil {
		return
	}
	sqlQuery := string(bytes)
	sqlQuery = strings.Replace(sqlQuery, "{{from}}", lib.ToYMDHMSDate(from), -1)
	sqlQuery = strings.Replace(sqlQuery, "{{to}}", lib.ToYMDHMSDate(to), -1)

	// Execute SQL
	rows := lib.QuerySQLWithErr(c, ctx, sqlQuery)
	defer rows.Close()

	// Now unknown rows, with unknown types
	columns, err := rows.Columns()
	if err != nil {
		return
	}

	// Vals to hold any type as []interface{}
	vals := make([]interface{}, len(columns))
	for i := range columns {
		vals[i] = new(sql.RawBytes)
	}

	// Get results into slices of slices of any type
	var results [][]interface{}
	for rows.Next() {
		err = rows.Scan(vals...)
		if err != nil {
			return
		}
		// We need to iterate row and get columns types
		rowSlice := []interface{}{}
		for _, val := range vals {
			var value interface{}
			if val != nil {
				value = string(*val.(*sql.RawBytes))
				iValue, err := strconv.Atoi(value.(string))
				if err == nil {
					value = iValue
				}
			}
			rowSlice = append(rowSlice, value)
		}
		results = append(results, rowSlice)
	}
	err = rows.Err()
	if err != nil {
		return
	}
	result = results
	return
}

// Add event
// eid, etype, aid, rid, public, created_at, aname, rname, orgid
func addEvent(con *sql.DB, ctx *lib.Ctx, args ...interface{}) (err error) {
	if len(args) != 9 {
		err = fmt.Errorf("addEvent: expects 9 variadic parameters")
		return
	}
	_, err = lib.ExecSQL(
		con,
		ctx,
		"insert into gha_events("+
			"id, type, actor_id, repo_id, public, created_at, "+
			"dup_actor_login, dup_repo_name, org_id) "+lib.NValues(9),
		args...,
	)
	return
}

// Add issue event label
// iid, eid, lid, lname, created_at
func addIssueEventLabel(con *sql.DB, ctx *lib.Ctx, args ...interface{}) (err error) {
	if len(args) != 11 {
		err = fmt.Errorf("addIssueEventLabel: expects 11 variadic parameters, got %v", len(args))
		return
	}
	_, err = lib.ExecSQL(
		con,
		ctx,
		"insert into gha_issues_events_labels("+
			"issue_id, event_id, label_id, label_name, created_at, "+
			"repo_id, repo_name, actor_id, actor_login, type, issue_number"+
			") "+lib.NValues(11),
		args...,
	)
	return
}

// Add text
// eid, body, created_at
func addText(con *sql.DB, ctx *lib.Ctx, args ...interface{}) (err error) {
	if len(args) != 3 {
		err = fmt.Errorf("addText: expects 3 variadic parameters")
		return
	}
	// STUB duplicated values for now
	args = append(args, []interface{}{0, "", 0, "", "D"}...)
	_, err = lib.ExecSQL(
		con,
		ctx,
		"insert into gha_texts("+
			"event_id, body, created_at, "+
			"repo_id, repo_name, actor_id, actor_login, type"+
			") "+lib.NValues(8),
		args...,
	)
	return
}

// Add PR
// prid, eid, uid, merged_id, assignee_id, num, state, title, body, created_at, closed_at, merged_at, merged
func addPR(con *sql.DB, ctx *lib.Ctx, args ...interface{}) (err error) {
	if len(args) != 17 {
		err = fmt.Errorf("addPR: expects 17 variadic parameters, got %v", len(args))
		return
	}

	newArgs := lib.AnyArray{
		args[0], // PR.id
		args[1], // event.ID
		args[2], // user.ID
		"250aac33d5aae922aac08bba4f06bd139c1c8994", // base SHA
		"9c31bcbc683a491c3d4122adcfe4caaab6e2d0fc", // head SHA
		args[3], // MergedBy.ID
		args[4], // Assignee.ID
		nil,
		args[5],    // PR.Number
		args[6],    // PR.State (open, closed)
		false,      // PR.Locked
		args[7],    // PR.Title
		args[8],    // PR.Body
		args[9],    // PR.CreatedAt
		time.Now(), // PR.UpdatedAt
		args[10],   // PR.ClosedAt
		args[11],   // PR.MergedAt
		"9c31bcbc683a491c3d4122adcfe4caaab6e2d0fc", // PR.MergeCommitSHA
		args[12],   // PR.Merged
		true,       // PR.mergable
		true,       // PR.Rebaseable
		"clean",    // PR.MergeableState (nil, unknown, clean, unstable, dirty)
		1,          // PR.Comments
		1,          // PR.ReviewComments
		true,       // PR.MaintainerCanModify
		1,          // PR.Commits
		1,          // PR.additions
		1,          // PR.Deletions
		1,          // PR.ChangedFiles
		args[15],   // Duplicate data starts here: ev.Actor.ID
		args[16],   // ev.Actor.Login
		args[13],   // ev.Repo.ID
		args[14],   // ev.Repo.Name
		"T",        // ev.Type
		time.Now(), // ev.CreatedAt
		"",         // PR.User.Login
		nil,        // PR.Assignee.Login
		nil,        // PR.MergedBy.Login
	}
	_, err = lib.ExecSQL(
		con,
		ctx,
		"insert into gha_pull_requests("+
			"id, event_id, user_id, base_sha, head_sha, merged_by_id, assignee_id, milestone_id, "+
			"number, state, locked, title, body, created_at, updated_at, closed_at, merged_at, "+
			"merge_commit_sha, merged, mergeable, rebaseable, mergeable_state, comments, "+
			"review_comments, maintainer_can_modify, commits, additions, deletions, changed_files, "+
			"dup_actor_id, dup_actor_login, dup_repo_id, dup_repo_name, dup_type, dup_created_at, "+
			"dup_user_login, dupn_assignee_login, dupn_merged_by_login) "+lib.NValues(38),
		newArgs...,
	)
	return
}

// Add Issue PR
// issue_id, pr_id, number, repo_id, repo_name, created_at
func addIssuePR(con *sql.DB, ctx *lib.Ctx, args ...interface{}) (err error) {
	if len(args) != 6 {
		err = fmt.Errorf("addIssuePR: expects 6 variadic parameters, got %v", len(args))
		return
	}
	_, err = lib.ExecSQL(
		con,
		ctx,
		"insert into gha_issues_pull_requests("+
			"issue_id, pull_request_id, number, repo_id, repo_name, created_at"+
			") "+lib.NValues(6),
		args...,
	)
	return
}

// Create data for opened to merged metric
func setupOpenedToMergedMetric(con *sql.DB, ctx *lib.Ctx) (err error) {
	ft := testlib.YMDHMS

	// PRs to add
	// prid, eid, uid, merged_id, assignee_id, num, state, title, body, created_at, closed_at, merged_at, merged
	// repo_id, repo_name, actor_id, actor_login
	prs := [][]interface{}{
		{1, 1, 1, 1, 1, 1, "closed", "PR 1", "Body PR 1", ft(2017, 7, 1), ft(2017, 7, 3), ft(2017, 7, 3), true, 1, "R1", 1, "A1"}, // average of PR 1-6 created -> merged is 48 hours
		{2, 2, 1, 1, 1, 2, "closed", "PR 2", "Body PR 2", ft(2017, 7, 2), ft(2017, 7, 3), ft(2017, 7, 3), true, 1, "R1", 1, "A1"},
		{3, 3, 1, 1, 1, 3, "closed", "PR 3", "Body PR 3", ft(2017, 7, 3), ft(2017, 7, 6), ft(2017, 7, 6), true, 1, "R1", 1, "A1"},
		{4, 4, 1, 1, 1, 4, "closed", "PR 4", "Body PR 4", ft(2017, 7, 4), ft(2017, 7, 5, 21, 15), ft(2017, 7, 5, 21, 15), true, 1, "R1", 1, "A1"},
		{5, 5, 1, 1, 1, 5, "closed", "PR 5", "Body PR 5", ft(2017, 7, 5), ft(2017, 7, 6, 20), ft(2017, 7, 6, 20), true, 1, "R1", 1, "A1"},
		{6, 6, 1, 1, 1, 6, "closed", "PR 6", "Body PR 6", ft(2017, 7, 6), ft(2017, 7, 8, 6, 45), ft(2017, 7, 8, 6, 45), true, 1, "R1", 1, "A1"},
		{7, 7, 1, 1, 1, 7, "closed", "PR 7", "Body PR 7", ft(2017, 6, 30), ft(2017, 7, 10), ft(2017, 7, 10), true, 1, "R1", 1, "A1"}, // skipped because not created in Aug
		{8, 1, 1, nil, 1, 8, "closed", "PR 8", "Body PR 8", ft(2017, 7, 2), ft(2017, 7, 8), nil, true, 1, "R1", 1, "A1"},             // Skipped because not merged
		{9, 1, 1, nil, 1, 9, "open", "PR 9", "Body PR 9", ft(2017, 7, 8), nil, nil, true, 1, "R1", 1, "A1"},                          // Skipped because not merged
	}

	// Issues/PRs to add
	// issue_id, pr_id, number, repo_id, repo_name, created_at
	iprs := [][]interface{}{
		{1, 1, 1, 1, "R1", ft(2017, 7, 1)},
		{2, 2, 2, 1, "R1", ft(2017, 7, 2)},
		{3, 3, 3, 1, "R1", ft(2017, 7, 3)},
		{4, 4, 4, 1, "R1", ft(2017, 7, 4)},
		{5, 5, 5, 1, "R1", ft(2017, 7, 5)},
		{6, 6, 6, 1, "R1", ft(2017, 7, 6)},
		{7, 7, 7, 1, "R1", ft(2017, 6, 30)},
		{8, 8, 8, 1, "R1", ft(2017, 7, 2)},
		{9, 9, 9, 1, "R1", ft(2017, 7, 8)},
	}

	// Add PRs
	for _, pr := range prs {
		err = addPR(con, ctx, pr...)
		if err != nil {
			return
		}
	}

	// Add Issue PRs
	for _, ipr := range iprs {
		err = addIssuePR(con, ctx, ipr...)
		if err != nil {
			return
		}
	}

	return
}

// Create data for (All) PRs merged metrics
func setupPRsMergedMetric(con *sql.DB, ctx *lib.Ctx) (err error) {
	ft := testlib.YMDHMS

	// Events to add
	// eid, etype, aid, rid, public, created_at, aname, rname, orgid
	events := [][]interface{}{
		{1, "T", 1, 1, true, ft(2017, 7, 1), "Actor 1", "Repo 1", 1},
		{2, "T", 1, 2, true, ft(2017, 7, 2), "Actor 1", "Repo 2", 1},
		{3, "T", 2, 3, true, ft(2017, 7, 3), "Actor 2", "Repo 3", nil},
		{4, "T", 2, 1, true, ft(2017, 7, 4), "Actor 2", "Repo 1", 1},
		{5, "T", 3, 1, true, ft(2017, 7, 5), "Actor 3", "Repo 1", 1},
		{6, "T", 4, 2, true, ft(2017, 7, 6), "Actor 4", "Repo 2", 1},
		{7, "T", 1, 1, true, ft(2017, 8), "Actor 1", "Repo 1", 1},
		{8, "T", 2, 2, true, ft(2017, 7, 7), "Actor 2", "Repo 2", 1},
		{9, "T", 3, 3, true, ft(2017, 7, 8), "Actor 3", "Repo 3", nil},
	}

	// PRs to add
	// prid, eid, uid, merged_id, assignee_id, num, state, title, body, created_at, closed_at, merged_at, merged
	// repo_id, repo_name, actor_id, actor_login
	prs := [][]interface{}{
		{1, 1, 1, 1, 1, 1, "closed", "PR 1", "Body PR 1", ft(2017, 6, 20), ft(2017, 7, 1), ft(2017, 7, 1), true, 1, "Repo 1", 1, "Actor 1"},
		{2, 5, 3, 2, 3, 2, "closed", "PR 2", "Body PR 2", ft(2017, 7, 1), ft(2017, 7, 5), ft(2017, 7, 5), true, 1, "Repo 1", 3, "Actor 3"},
		{3, 4, 2, 3, 2, 3, "closed", "PR 3", "Body PR 3", ft(2017, 7, 2), ft(2017, 7, 4), ft(2017, 7, 4), true, 1, "Repo 1", 2, "Actor 2"},
		{4, 2, 2, 4, 4, 4, "closed", "PR 4", "Body PR 4", ft(2017, 6, 10), ft(2017, 7, 2), ft(2017, 7, 2), true, 2, "Repo 2", 1, "Actor 1"},
		{5, 6, 4, 4, 4, 5, "closed", "PR 5", "Body PR 5", ft(2017, 7, 5), ft(2017, 7, 6), ft(2017, 7, 6), true, 2, "Repo 2", 4, "Actor 4"},
		{6, 3, 2, 2, 4, 6, "closed", "PR 6", "Body PR 6", ft(2017, 7, 2), ft(2017, 7, 3), ft(2017, 7, 3), true, 3, "Repo 3", 2, "Actor 2"},
		{7, 7, 1, 1, 1, 7, "closed", "PR 7", "Body PR 7", ft(2017, 7, 1), ft(2017, 8), ft(2017, 8), true, 1, "Repo 1", 1, "Actor 1"},
		{8, 8, 2, nil, 2, 8, "closed", "PR 8", "Body PR 8", ft(2017, 7, 7), ft(2017, 7, 8), nil, true, 2, "Repo 2", 2, "Actor 2"},
		{9, 9, 3, nil, 1, 9, "open", "PR 9", "Body PR 9", ft(2017, 7, 8), nil, nil, true, 3, "Repo 3", 3, "Actor 3"},
	}

	// Add events
	for _, event := range events {
		err = addEvent(con, ctx, event...)
		if err != nil {
			return
		}
	}

	// Add PRs
	for _, pr := range prs {
		err = addPR(con, ctx, pr...)
		if err != nil {
			return
		}
	}

	return
}

// Create data for SIG mentions metric
func setupSigMentionsMetric(con *sql.DB, ctx *lib.Ctx) (err error) {
	ft := testlib.YMDHMS

	// texts to add
	// eid, body, created_at
	texts := [][]interface{}{
		{1, `Hello @kubernetes/sig-group-1`, ft(2017, 7, 1)},
		{2, `@kubernetes/sig-group-1-bugs, do you know about this bug?`, ft(2017, 7, 2)},
		{3, `kubernetes/sig-group missing @ - not counted`, ft(2017, 7, 3)},
		{4, `@kubernetes/sig-group-1- not included, group cannot end with -`, ft(2017, 7, 4)},
		{5, `XYZ@kubernetes/sig-group-1 - not included, there must be white space or beggining of string before @`, ft(2017, 7, 5)},
		{6, " \t@kubernetes/sig-group-1-feature-request: we should consider adding new bot... \n ", ft(2017, 7, 6)},
		{7, `Hi @kubernetes/sig-group2-bugs; I wanted to report bug`, ft(2017, 7, 7)},
		{8, `I have reviewed this PR, @kubernetes/sig-group2-pr-reviews ping!`, ft(2017, 7, 8)},
		{9, `Is there a @kubernetes/sig-a-b-c? Or maybe @kubernetes/sig-a-b-c-bugs?`, ft(2017, 7, 9)}, // counts as single mention.
		{10, `@kubernetes/sig-group2-bugs? @kubernetes/sig-group2? @kubernetes/sig-group2-pr-review? anybody?`, ft(2017, 7, 10)},
		{11, `@kubernetes/sig-group2-feature-requests out of test range`, ft(2017, 8, 11)},
	}

	// Add texts
	for _, text := range texts {
		err = addText(con, ctx, text...)
		if err != nil {
			return
		}
	}

	return
}

// Create data for reviewers metric
func setupReviewersMetric(con *sql.DB, ctx *lib.Ctx) (err error) {
	ft := testlib.YMDHMS

	// Events to add
	// eid, etype, aid, rid, public, created_at, aname, rname, orgid
	events := [][]interface{}{
		{1, "T", 1, 1, true, ft(2017, 7, 10), "Actor 1", "Repo 1", 1},
		{2, "T", 2, 2, true, ft(2017, 7, 11), "Actor 2", "Repo 2", 1},
		{3, "T", 3, 1, true, ft(2017, 7, 12), "Actor 3", "Repo 1", 1},
		{4, "T", 4, 3, true, ft(2017, 7, 13), "Actor 4", "Repo 3", 2},
		{5, "T", 5, 2, true, ft(2017, 7, 14), "Actor 5", "Repo 2", 1},
		{6, "T", 5, 2, true, ft(2017, 7, 15), "Actor 5", "Repo 2", 1},
		{7, "T", 3, 2, true, ft(2017, 7, 16), "Actor 5", "Repo 2", 1},
		{8, "T", 6, 4, true, ft(2017, 7, 17), "Actor 6", "Repo 4", 2},
		{9, "T", 7, 5, true, ft(2017, 7, 18), "Actor 7", "Repo 5", nil},
		{10, "T", 8, 5, true, ft(2017, 7, 19), "Actor 8", "Repo 5", nil},
		{11, "T", 9, 5, true, ft(2017, 7, 20), "Actor 9", "Repo 5", nil},
		{12, "T", 9, 5, true, ft(2017, 8, 10), "Actor X", "Repo 5", nil},
		{13, "T", 10, 1, true, ft(2017, 7, 21), "Actor Y", "Repo 1", 1},
	}

	// Issue Event Labels to add
	// iid, eid, lid, lname, created_at
	// repo_id, repo_name, actor_id, actor_login, type, issue_number
	iels := [][]interface{}{
		{1, 1, 1, "lgtm", ft(2017, 7, 10), 1, "Repo 1", 1, "Actor 1", "T", 1}, // 4 labels match, but 5 and 6 have the same actor, so 3 reviewers here.
		{2, 2, 2, "lgtm", ft(2017, 7, 11), 2, "Repo 2", 2, "Actor 2", "T", 2},
		{5, 5, 5, "lgtm", ft(2017, 7, 14), 2, "Repo 2", 5, "Actor 5", "T", 5},
		{6, 6, 6, "lgtm", ft(2017, 7, 15), 2, "Repo 2", 5, "Actor 5", "T", 6},
		{6, 9, 1, "lgtm", ft(2017, 7, 18), 5, "Repo 5", 7, "Actor 7", "T", 6},      // Not counted because it belongs to issue_id (6) which received LGTM in previous line
		{10, 10, 10, "other", ft(2017, 7, 19), 5, "Repo 5", 8, "Actor 8", "T", 10}, // Not LGTM
		{12, 12, 1, "lgtm", ft(2017, 8, 10), 5, "Repo 5", 9, "Actor 9", "T", 12},   // Out of date range
	}

	// texts to add
	// eid, body, created_at
	texts := [][]interface{}{
		{3, "/lgtm", ft(2017, 7, 12)},   // 7 gives actor already present in issue event lables
		{4, " /LGTM ", ft(2017, 7, 13)}, // so 4 reviewers here, sum 7
		{7, " /LGtm ", ft(2017, 7, 16)},
		{8, "\t/lgTM\n", ft(2017, 7, 17)},
		{11, "/lGtM with additional text", ft(2017, 7, 20)}, // additional text causes this line to be skipped
		{13, "Line 1\n/lGtM\nLine 2", ft(2017, 7, 21)},      // This is included because /LGTM is in its own line only eventually surrounded by whitespace
	}

	// Add events
	for _, event := range events {
		err = addEvent(con, ctx, event...)
		if err != nil {
			return
		}
	}

	// Add issue event labels
	for _, iel := range iels {
		err = addIssueEventLabel(con, ctx, iel...)
		if err != nil {
			return
		}
	}

	// Add texts
	for _, text := range texts {
		err = addText(con, ctx, text...)
		if err != nil {
			return
		}
	}

	return
}