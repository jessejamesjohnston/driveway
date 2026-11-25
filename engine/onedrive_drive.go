// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/rclone/rclone/fs/fshttp"
)

// After Driveway's own OneDrive sign-in, Driveway picks the drive itself
// instead of passing on rclone's questions. rclone lists everything
// Microsoft returns for /me/drives, and a personal account has hidden system
// drives there (ODCMetadataArchive, a Bundles_ drive, document libraries
// named by ID). rclone offers the first of those first, and it fails to
// open. The account's own drive is /me/drive: Driveway sets
// it, and the server is ready without a question. Only if Microsoft won't
// say does the person pick, from the real drives, by plain names.

// graphBase is Microsoft Graph for the global cloud, the only one Merriment
// Labs' registration covers. Tests point it at a stand-in.
var graphBase = "https://graph.microsoft.com/v1.0"

// oneDrivePickState is Driveway's own question (not rclone's): which drive.
const oneDrivePickState = "*driveway:onedrive-drive"

type graphDrive struct {
	ID        string `json:"id"`
	DriveType string `json:"driveType"`
	Name      string `json:"name"`
	Owner     struct {
		User struct {
			DisplayName string `json:"displayName"`
		} `json:"user"`
	} `json:"owner"`
}

// A pick waiting for the person's answer, by setup id.
var (
	pickMu  sync.Mutex
	pending = map[string]pendingPick{}
)

type pendingPick struct {
	m      map[string]any
	drives []graphDrive
}

func clearPick(id string) {
	pickMu.Lock()
	delete(pending, id)
	pickMu.Unlock()
}

// accessToken is the access token in rclone's token JSON.
func accessToken(m map[string]any) string {
	var t struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal([]byte(fmtValue(m["token"])), &t)
	return t.AccessToken
}

// graphGet calls Microsoft Graph with the new sign-in. It returns the HTTP
// status, with err set only when Graph couldn't be reached.
func graphGet(c context.Context, base, token, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(c, "GET", base+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := fshttp.NewClient(c).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return http.StatusBadGateway, nil
		}
	}
	return resp.StatusCode, nil
}

// realDrive: a drive a person means by "my OneDrive". Microsoft also lists
// system drives: document libraries (SharePoint, and on personal accounts
// libraries named by an ID), ODCMetadataArchive, and Bundles_ (the photo
// albums' store).
func realDrive(d graphDrive) bool {
	if d.ID == "" || (d.DriveType != "personal" && d.DriveType != "business") {
		return false
	}
	n := strings.ToLower(d.Name)
	return !strings.HasPrefix(n, "bundles_") && !strings.HasPrefix(n, "odcmetadata")
}

// driveLabel is a drive as the person knows it.
func driveLabel(d graphDrive, sameType bool) string {
	label := "Your OneDrive"
	if d.DriveType == "business" {
		label = "Your work or school OneDrive"
	}
	if sameType {
		// Two of a kind: say whose, or which.
		if who := d.Owner.User.DisplayName; who != "" {
			return label + " (" + who + ")"
		}
		if d.Name != "" {
			return label + " (" + d.Name + ")"
		}
	}
	return label
}

// pickOneDrive sets the drive after Driveway's own OneDrive sign-in: the one
// the server had (signing in again), else the account's own drive, else the
// only real one; with several real drives and no answer from Microsoft about
// which is the account's own, it asks.
func pickOneDrive(id string, m map[string]any) (string, error) {
	clearPick(id)
	if fmtValue(m["drive_id"]) != "" && fmtValue(m["drive_type"]) != "" {
		return finishOneDrive(id, m, graphDrive{ID: fmtValue(m["drive_id"]), DriveType: fmtValue(m["drive_type"])})
	}
	token := accessToken(m)
	// graph_url: a stand-in for Graph (tools/signin-harness.sh), as auth_url
	// and token_url are for the sign-in.
	base := graphBase
	if s := fmtValue(m["graph_url"]); s != "" {
		base = s
	}
	c := ctx()
	var me graphDrive
	status, err := graphGet(c, base, token, "/me/drive", &me)
	if err != nil {
		return "", classify(err)
	}
	if status == http.StatusUnauthorized {
		return "", tag("signin", errors.New("OneDrive didn't accept the new sign-in"))
	}
	if status/100 == 2 && realDrive(me) {
		return finishOneDrive(id, m, me)
	}
	var list struct {
		Value []graphDrive `json:"value"`
	}
	status, err = graphGet(c, base, token, "/me/drives", &list)
	if err != nil {
		return "", classify(err)
	}
	var real []graphDrive
	if status/100 == 2 {
		for _, d := range list.Value {
			if realDrive(d) {
				real = append(real, d)
			}
		}
	}
	switch len(real) {
	case 0:
		return "", tag("setup", fmt.Errorf("OneDrive didn't say which drive is this account's (/me/drive %d, /me/drives %d)", status, len(list.Value)))
	case 1:
		return finishOneDrive(id, m, real[0])
	}
	pickMu.Lock()
	pending[id] = pendingPick{m: m, drives: real}
	pickMu.Unlock()
	types := map[string]int{}
	for _, d := range real {
		types[d.DriveType]++
	}
	q := question{State: oneDrivePickState, Name: "config_driveid", Help: "Which OneDrive should Driveway open?",
		Required: true, Exclusive: true, Friendly: true}
	for _, d := range real {
		q.Examples = append(q.Examples, choice{Value: d.ID, Help: driveLabel(d, types[d.DriveType] > 1)})
	}
	b, _ := json.Marshal(q)
	return string(b), nil
}

// answerOneDrive takes the person's pick.
func answerOneDrive(id, answer string) (string, error) {
	pickMu.Lock()
	p, ok := pending[id]
	delete(pending, id)
	pickMu.Unlock()
	if !ok {
		return "", tag("setup", errors.New("no OneDrive pick is waiting"))
	}
	for _, d := range p.drives {
		if d.ID == answer {
			return finishOneDrive(id, p.m, d)
		}
	}
	return "", tag("setup", errors.New("that drive isn't one OneDrive offered"))
}

// finishOneDrive keeps the drive and the sign-in as the server's settings.
// rclone's own steps would only ask what is settled here, so they don't run.
func finishOneDrive(id string, m map[string]any, d graphDrive) (string, error) {
	m["drive_id"] = d.ID
	m["drive_type"] = d.DriveType
	delete(m, "graph_url")
	for k := range m {
		// Setup answers (config_type, config_refresh_token) are not settings.
		if strings.HasPrefix(k, "config_") {
			delete(m, k)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", tag("other", err)
	}
	if err := SetRemote(id, "onedrive", string(b)); err != nil {
		return "", err
	}
	return `{"done":true}`, nil
}
