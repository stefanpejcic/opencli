// ##############################################################################
// Script Name: internal/commands/plan/edit.go
// Description: Edit an existing hosting plan (Package) and modify its parameters.
// Usage: opencli plan-edit id=<ID> name="<TEXT>" description="<TEXT>" emails=<COUNT> ftp=<COUNT> domains=<COUNT> websites=<COUNT> disk=<COUNT> inodes=<COUNT> databases=<COUNT> cpu=<COUNT> ram=<COUNT> bandwidth=<COUNT> feature_set=<NAME> max_email_quota=<COUNT> max_hourly_email=<COUNT> [--debug]
// Author: Stefan Pejcic
// Created: 10.04.2024
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
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/redis"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "plan-edit",
		Desc:  "Edit an existing hosting plan (Package) and modify its parameters.",
		Usage: `opencli plan-edit id=<ID> name="<TEXT>" description="<TEXT>" emails=<COUNT> ftp=<COUNT> domains=<COUNT> websites=<COUNT> disk=<COUNT> inodes=<COUNT> databases=<COUNT> cpu=<COUNT> ram=<COUNT> bandwidth=<COUNT> feature_set=<NAME> max_email_quota=<COUNT> max_hourly_email=<COUNT> [--debug]`,
		Run:   edit,
	})
}

func editUsage() int {
	fmt.Print(`Usage: /usr/local/opencli/plan/edit.sh [OPTIONS]

Options:
  id=<id>                Set the plan ID
  name='<name>'          Set the plan name
  description='<text>'   Set the plan description
  emails=<num>           Set the email limit
  max_email_quota=<num>  Set the max email mailbox size
  max_hourly_email=<num> Set the max hourly emails sent
  ftp=<num>              Set the FTP limit
  domains=<num>          Set the domain limit
  websites=<num>         Set the website limit
  disk=<num>             Set the disk limit (in GB)
  inodes=<num>           Set the inodes limit
  databases=<num>        Set the databases limit
  cpu=<num>              Set the CPU limit
  ram=<num>              Set the RAM limit (in GB)
  bandwidth=<num>        Set the bandwidth limit (in Mbps)
  feature_set=<name>     Name of the feature set to be used
  --debug                Enable debug mode

Example:
  opencli plan-edit --debug id=1 name="New Plan" description="This is a new plan" emails=100 max_email_quota="10G" max_hourly_email=1000 ftp=50 domains=20 websites=30 disk=100 inodes=100000 databases=10 cpu=4 ram=8 bandwidth=100 feature_set=default
`)
	return 1
}

var usernameRe = regexp.MustCompile(`"username": "[^"]*`)

func edit(args []string) int {
	db.Require()
	if len(args) < 13 {
		return editUsage()
	}
	debug := false
	var id, name, description, emails, ftp, domains, websites, disk, inodes, databases, cpu, ram, bandwidth, quota, hourly string
	feature := "default"
	quota = "0"
	hourly = "0"
	for _, a := range args {
		k, v, has := strings.Cut(a, "=")
		if !has {
			k = a
		}
		switch {
		case a == "--debug":
			debug = true
		case !has:
			fmt.Println("Unknown argument: " + a)
			return editUsage()
		case k == "id":
			id = v
		case k == "name":
			name = v
		case k == "description":
			description = v
		case k == "emails":
			emails = v
		case k == "ftp":
			ftp = v
		case k == "domains":
			domains = v
		case k == "websites":
			websites = v
		case k == "disk":
			disk = v
		case k == "inodes":
			inodes = v
		case k == "databases":
			databases = v
		case k == "cpu":
			cpu = v
		case k == "ram":
			ram = v
		case k == "bandwidth":
			bandwidth = v
		case k == "feature_set":
			feature = v
		case k == "max_email_quota":
			quota = v
		case k == "max_hourly_email":
			hourly = v
		default:
			fmt.Println("Unknown argument: " + a)
			return editUsage()
		}
	}

	if db.Cmd("SELECT id FROM plans WHERE id='"+id+"';", "-N", "-B").Str() == "" {
		fmt.Printf("ERROR: Plan with id '%s' does not exist.\n", id)
		return 1
	}
	if !checkIntegers([][2]string{{"plan_id", id}, {"ftp_limit", ftp}, {"emails_limit", emails}, {"max_hourly_email", hourly}, {"domains_limit", domains}, {"websites_limit", websites}, {"disk_limit", disk}, {"inodes_limit", inodes}, {"db_limit", databases}, {"bandwidth", bandwidth}}) {
		return 1
	}
	// the normalised quota isn't kept here, a bare number stays as typed
	if _, ok := quotaNorm(quota); !ok {
		fmt.Printf("Error: %s must be a number with optional unit (B|k|M|G|T)\n", quota)
		return 1
	}
	if !checkCPURAM(cpu, ram) {
		return 1
	}
	if !checkNameFeature(name, feature) {
		return 1
	}

	if !editUpdate(id, name, description, ftp, emails, domains, websites, disk, inodes, databases, cpu, ram, bandwidth, feature, quota, hourly, debug) {
		return 1
	}
	// only what a plan edit can change, never flush the whole cache
	redis.DropKey(
		"openpanel_cache_get_feature_set_name_by_plan_id:"+id,
		"openpanel_cache_load_features_for_plan_id:"+id,
		"openpanel_cache_query_plan_email_mailbox_limits:"+id,
	)
	redis.DropPattern(
		"openpanel_cache_get_user_details_with_plan:*",
		"openpanel_cache_get_feature_set_on_plan:*",
		"openpanel_cache_load_user_features:*",
		"openpanel_cache_query_plan_details_by_id:*",
	)
	return 0
}

func editUpdate(id, newName, description, ftp, emails, domains, websites, intDisk, inodes, databases, cpu, intRAM, bandwidth, feature, quota, hourly string, debug bool) bool {
	result := db.Cmd("SELECT name, disk_limit, inodes_limit, cpu, ram, bandwidth, max_hourly_email, feature_set FROM plans WHERE id='"+id+"'", "-N").Str()
	// split on tabs only, name can have spaces and disk_limit is "10 GB"
	f := strings.Split(result, "\t")
	for len(f) < 8 {
		f = append(f, "")
	}
	oldName, oldDiskRaw, oldInodes, oldCPU, oldRAM, oldBW, oldHourly, oldFeature := f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[7]
	oldDisk, _, _ := strings.Cut(oldDiskRaw, " ")
	oldDisk += " GB"

	disk := intDisk + " GB"
	ram := intRAM
	if !strings.HasSuffix(intRAM, "g") {
		ram = intRAM + "g"
	}

	if debug {
		fmt.Println("+===================================+")
		fmt.Println("| PLAN ID: " + id)
		fmt.Println("+===================================+")
		fmt.Println("Old Plan Name:        " + oldName)
		fmt.Println("Old Disk Limit:       " + oldDisk)
		fmt.Println("Old Inodes Limit:     " + oldInodes)
		fmt.Println("Old CPU:              " + oldCPU)
		fmt.Println("Old RAM:              " + oldRAM)
		fmt.Println("Old Bandwidth:        " + oldBW)
		fmt.Println("Old max_hourly_email: " + oldHourly)
		fmt.Println("Old feature set:      " + oldFeature)
		fmt.Println("+===================================+")
		fmt.Println("New plan information:")
		fmt.Println("Name:                 " + newName)
		fmt.Println("Description:          " + description)
		fmt.Println("Feature set:          " + feature)
		fmt.Println("Disk limit:           " + disk)
		fmt.Println("Inodes limit:         " + inodes)
		fmt.Println("CPU:                  " + cpu + " cores")
		fmt.Println("RAM:                  " + ram)
		fmt.Println("Bandwidth:            " + bandwidth)
		fmt.Println("FTP accounts:         " + ftp)
		fmt.Println("Email accounts:       " + emails)
		fmt.Println("Max email quota:      " + quota)
		fmt.Println("Max hourly email:     " + hourly)
		fmt.Println("Total domains:        " + domains)
		fmt.Println("Total websites:       " + websites)
		fmt.Println("Total databases:      " + databases)
		fmt.Println("+===================================+")
	}

	sql := fmt.Sprintf("UPDATE plans SET name='%s', description='%s', ftp_limit=%s, email_limit=%s, domains_limit=%s, websites_limit=%s, disk_limit='%s', inodes_limit=%s, db_limit=%s, cpu=%s, ram='%s', bandwidth=%s, feature_set='%s', max_email_quota='%s', max_hourly_email='%s' WHERE id='%s';",
		db.Escape(newName), db.Escape(description), ftp, emails, domains, websites, disk, inodes, databases, cpu, ram, bandwidth, db.Escape(feature), quota, hourly, id)
	if db.Exec(sql) != nil {
		fmt.Printf("ERROR: Failed to update plan id '%s'\n", id)
		return false
	}
	planName := db.Cmd("SELECT name FROM plans WHERE id='"+id+"'", "-N").Str()
	out, _ := sh.Opencli("plan-usage", planName, "--json").Out()
	count := len(usernameRe.FindAllString(out, -1))
	if count == 0 {
		fmt.Printf("Successfully updated plan id %s\n", id)
		return true
	}

	var flags []string
	if inodes != oldInodes || disk != oldDisk {
		flags = append(flags, "--dsk")
	} else if debug {
		fmt.Println("DEBUG: Disk & Inodes limits are not changed.")
	}
	switch {
	case oldCPU == cpu && oldRAM == ram:
		if debug {
			fmt.Println("DEBUG: CPU & RAM limits are not changed.")
		}
	case oldCPU != cpu && oldRAM != ram:
		if debug {
			fmt.Println("DEBUG: Both CPU or RAM limits are changed, applying new limits.")
		}
		flags = append(flags, "--cpu", "--ram")
	case oldCPU != cpu:
		if debug {
			fmt.Println("DEBUG: CPU limits are changed.")
		}
		flags = append(flags, "--cpu")
	default:
		if debug {
			fmt.Println("DEBUG: RAM limits are changed.")
		}
		flags = append(flags, "--ram")
	}
	if oldBW != bandwidth {
		if debug {
			fmt.Println("DEBUG: Port speed limit is changed, applying new bandwidth limit to the podman network.")
		}
		flags = append(flags, "--net")
	}
	if oldHourly != hourly {
		if debug {
			fmt.Println("DEBUG: max_hourly_email limit is changed, applying new limits for domains.")
		}
		flags = append(flags, "--email")
	}

	if len(flags) == 0 {
		fmt.Printf("Successfully updated plan id %s. You currently have %d users on this plan. New limits have been applied.\n", id, count)
		return true
	}
	fmt.Printf("Plan ID %s updated successfully. Applying new limits to %d users on this plan..\n", id, count)
	fmt.Println("")
	fmt.Println("You can track progress using the command:")
	logPath := "/tmp/opencli_plan_apply_" + time.Now().Format("20060102_150405") + ".log"
	fmt.Println("tail -f " + logPath)
	applyArgs := append([]string{"plan-apply", id}, flags...)
	applyArgs = append(applyArgs, "--all")
	if debug {
		fmt.Println("DEBUG: Running command: opencli plan-apply " + id + " " + strings.Join(flags, " ") + " --all --debug")
		applyArgs = append(applyArgs, "--debug")
	}
	if lf, err := os.Create(logPath); err == nil {
		sh.Opencli(applyArgs...).Stdout(lf).Stderr(lf).Background()
		lf.Close()
	}
	return true
}
