// ##############################################################################
// Script Name: internal/commands/plan/create.go
// Description: Create a new hosting plan (Package) and set its limits.
// Usage: opencli plan-create name="<TEXT>" description="<TEXT>" emails=<COUNT> ftp=<COUNT> domains=<COUNT> websites=<COUNT> disk=<COUNT> inodes=<COUNT> databases=<COUNT> cpu=<COUNT> ram=<COUNT> bandwidth=<COUNT> feature_set=<NAME> max_email_quota=<COUNT> max_hourly_email=<COUNT> [reseller=<USERNAME>]
// Author: Stefan Pejcic
// Created: 06.11.2023
// Last Modified: 20.11.2026
// Company: openpanel.com
// Copyright (c) openpanel.com
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
// ##############################################################################

package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/reseller"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "plan-create",
		Desc:  "Create a new hosting plan (Package) and set its limits.",
		Usage: `opencli plan-create name="<TEXT>" description="<TEXT>" emails=<COUNT> ftp=<COUNT> domains=<COUNT> websites=<COUNT> disk=<COUNT> inodes=<COUNT> databases=<COUNT> cpu=<COUNT> ram=<COUNT> bandwidth=<COUNT> feature_set=<NAME> max_email_quota=<COUNT> max_hourly_email=<COUNT> [reseller=<USERNAME>]`,
		Run:   create,
	})
}

func createHelp() int {
	fmt.Print(`Usage: opencli plan-create name="<TEXT>" description="<TEXT>" emails=<COUNT> ftp=<COUNT> domains=<COUNT> websites=<COUNT> disk=<COUNT> inodes=<COUNT> databases=<COUNT> cpu=<COUNT> ram=<COUNT> bandwidth=<COUNT> feature_set=<NAME> max_email_quota=<COUNT> max_hourly_email=<COUNT>

Arguments:
  name             - Name of the plan (string, no spaces).
  description      - Plan description (string, use quotes for multiple words).
  feature_set      - Name of the feature set used for users on this plan
  email_limit      - Max number of email accounts (integer, 0 for unlimited).
  max_email_quota  - Max size per email account (intiger followed by B|k|M|G|T, 0 for unlimited).
  max_hourly_email - Max number of outgoing emails that can be sent across all domains.
  ftp_limit        - Max number of FTP accounts (integer, 0 for unlimited).
  domains_limit    - Max number of domains (integer, 0 for unlimited).
  websites_limit   - Max number of websites (integer, 0 for unlimited).
  disk_limit       - Disk space limit in GB (integer).
  inodes_limit     - Max number of inodes (integer, 0 = unlimited).
  db_limit         - Max number of databases (integer, 0 for unlimited).
  cpu              - CPU core limit (integer).
  ram              - RAM limit in GB (integer).
  bandwidth        - Port speed in Mbit/s (integer).
  reseller         - (optional) Username of a reseller to grant access to this plan.

Example:
  opencli plan-create name=New Plan description=This is a new plan emails=100 ftp=50 domains=20 websites=30 disk=100 inodes=100000 databases=10 cpu=4 ram=8 bandwidth=100 feature_set=default max_email_quota=2G max_hourly_email=1000

`)
	return 1
}

func create(args []string) int {
	if len(args) < 13 {
		return createHelp()
	}
	db.Require()

	var name, description, emails, ftp, domains, websites, disk, inodes, databases, cpu, ram, bandwidth, resellerUser string
	feature := "default"
	quota := "0"
	hourly := "0"
	for _, a := range args {
		k, v, _ := strings.Cut(a, "=")
		if !strings.Contains(a, "=") {
			k = ""
		}
		switch k {
		case "reseller":
			resellerUser = v
		case "name":
			name = v
		case "description":
			description = v
		case "emails":
			emails = v
		case "ftp":
			ftp = v
		case "domains":
			domains = v
		case "websites":
			websites = v
		case "disk":
			disk = v
		case "inodes":
			inodes = v
		case "databases":
			databases = v
		case "cpu":
			cpu = v
		case "ram":
			ram = v
		case "bandwidth":
			bandwidth = v
		case "feature_set":
			feature = v
		case "max_email_quota":
			quota = v
		case "max_hourly_email":
			hourly = v
		default:
			fmt.Println("Unknown argument: " + a)
			return createHelp()
		}
	}

	if !checkNameFeature(name, feature) {
		return 1
	}
	esc := db.Escape(name)
	if db.Cmd("SELECT id FROM plans WHERE name='"+esc+"' LIMIT 1;", "-N", "-B").Str() != "" {
		fmt.Printf("Error: Plan name '%s' already exists. Please choose another name.\n", name)
		return 1
	}
	if !checkIntegers([][2]string{{"ftp_limit", ftp}, {"emails_limit", emails}, {"max_hourly_email", hourly}, {"domains_limit", domains}, {"websites_limit", websites}, {"disk_limit", disk}, {"inodes_limit", inodes}, {"db_limit", databases}, {"bandwidth", bandwidth}}) {
		return 1
	}
	q, ok := quotaNorm(quota)
	if !ok {
		fmt.Printf("Error: %s must be a number with optional unit (B|k|M|G|T)\n", quota)
		return 1
	}
	quota = q
	if !checkCPURAM(cpu, ram) {
		return 1
	}

	// reseller file is checked before anything gets inserted
	resellerFile := ""
	if resellerUser != "" {
		resellerFile = "/etc/openpanel/openadmin/resellers/" + resellerUser + ".json"
		b, err := os.ReadFile(resellerFile)
		if err != nil {
			fmt.Println("Error: Configuration file does not exist: " + resellerFile)
			return 1
		}
		if _, err := ojson.Parse(string(b)); err != nil {
			fmt.Println("Error: Invalid JSON in " + resellerFile)
			return 1
		}
	}

	if n, err := strconv.ParseInt(inodes, 10, 64); err == nil && n < 0 {
		inodes = "0"
	}
	sql := fmt.Sprintf("INSERT INTO plans (name, description, email_limit, ftp_limit, domains_limit, websites_limit, disk_limit, inodes_limit, db_limit, cpu, ram, bandwidth, feature_set, max_email_quota, max_hourly_email) VALUES ('%s', '%s', %s, %s, %s, %s, '%s', %s, %s, %s, '%s', %s, '%s', '%s', '%s');",
		esc, db.Escape(description), emails, ftp, domains, websites, disk+" GB", inodes, databases, cpu, ram+"g", bandwidth, db.Escape(feature), quota, hourly)
	if db.Exec(sql) != nil {
		fmt.Println("Failed to create plan: " + name)
		return 0
	}
	if resellerFile != "" {
		createGrant(esc, resellerFile)
	}
	fmt.Printf("Plan %s created successfully.\n", name)
	return 0
}

// createGrant adds the new plan id to the reseller's allowed_plans and own_plans
func createGrant(escName, file string) {
	id := ""
	for _, l := range strings.Split(db.Cmd("SELECT id FROM plans WHERE name='"+escName+"' LIMIT 1;", "-N", "-B").NoErr().Str(), "\n") {
		if isDigits(l) {
			id = l
			break
		}
	}
	if id == "" {
		fmt.Println("Warning: failed to retrieve plan ID from the database and assign it to the reseller user.")
		return
	}
	o, err := reseller.Load(file)
	if err != nil {
		return
	}
	// own_plans is what keeps the plan allowed when a reseller plan gets reapplied
	own := reseller.OwnPlans(file)
	cur, _ := o.Get("allowed_plans")
	if cur == nil || cur == false {
		cur = []any{}
	}
	allowed := parseNums(reseller.PlansJSON(ojson.Compact(cur)))
	o.Set("allowed_plans", withID(allowed, id))
	o.Set("own_plans", withID(parseNums(own), id))
	tmp, err := os.CreateTemp("", "tmp.*")
	if err != nil {
		return
	}
	tmp.WriteString(ojson.Pretty(o) + "\n")
	tmp.Close()
	if os.Rename(tmp.Name(), file) != nil {
		// mv across filesystems copies into a new file with the temp file's 0600 mode
		b, _ := os.ReadFile(tmp.Name())
		os.Remove(file)
		if os.WriteFile(file, b, 0o600) == nil {
			os.Remove(tmp.Name())
		}
	}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseNums(s string) []any {
	v, _ := ojson.Parse(s)
	arr, _ := v.([]any)
	return arr
}

func withID(list []any, id string) []any {
	for _, it := range list {
		if ojson.Text(it) == id {
			return list
		}
	}
	return append(list, json.Number(id))
}
