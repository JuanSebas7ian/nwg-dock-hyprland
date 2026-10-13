package iphone

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// What fills the device besides the camera roll, read from its local backup (read-only): each backup file has its
// app ("domain") and path in Manifest.db and its bytes on disk. Apps keep their files in their own sandbox, so a PC
// cannot delete them (except apps that share files): the report says what is heavy and where to delete it on the
// device. WhatsApp gets its own part (chat names and attachment sizes and dates from ChatStorage.sqlite; never
// message text), since it is usually the largest. Caches are not in a backup: those are in the app sizes.

// StorageArea is one app or system area of the backup.
type StorageArea struct {
	Domain string `json:"domain"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
	Hint   string `json:"hint,omitempty"` // where to delete it on the device
}

// ChatMedia is one WhatsApp chat's attachments.
type ChatMedia struct {
	Name  string `json:"name"`
	Group bool   `json:"group"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

// HeavyAttachment is one large WhatsApp attachment.
type HeavyAttachment struct {
	Chat  string `json:"chat"`
	Kind  string `json:"kind"` // video, document, photo, audio, sticker
	Ext   string `json:"ext"`
	Bytes int64  `json:"bytes"`
	Date  string `json:"date,omitempty"`
}

// StorageReport is State/<udid>/storage.json.
type StorageReport struct {
	Time       string            `json:"time"`
	BackupTime string            `json:"backupTime"`
	Total      int64             `json:"total"`
	Areas      []StorageArea     `json:"areas"`
	WhatsApp   int64             `json:"whatsapp"`
	Chats      []ChatMedia       `json:"chats"`
	Kinds      map[string]int64  `json:"kinds"`
	Heavy      []HeavyAttachment `json:"heavy"`
}

const waDomain = "AppDomainGroup-group.net.whatsapp.WhatsApp.shared"

// sqliteJSON runs a read-only query with the sqlite3 tool and decodes its JSON rows.
func sqliteJSON(run Runner, db, query string, out any) error {
	b, err := run("sqlite3", "-readonly", "-json", db, query)
	if err != nil {
		return fmt.Errorf("sqlite3: %s", lastLine(string(b)))
	}
	if strings.TrimSpace(string(b)) == "" {
		return nil
	}
	return json.Unmarshal(b, out)
}

func kindOf(ext string) string {
	switch ext {
	case ".mp4", ".mov", ".m4v", ".3gp":
		return "video"
	case ".jpg", ".jpeg", ".png", ".heic":
		return "photo"
	case ".opus", ".m4a", ".mp3", ".aac", ".ogg":
		return "audio"
	case ".webp", ".was":
		return "sticker"
	}
	return "document"
}

// areaName gives a backup domain a readable name and where to free it.
func areaName(domain string, apps map[string]string) (string, string) {
	switch {
	case domain == "CameraRollDomain":
		return "Photos and videos", "Free up space tab"
	case domain == waDomain || strings.Contains(domain, "net.whatsapp"):
		return "WhatsApp", "WhatsApp › Settings › Storage and Data › Manage Storage"
	case domain == "MediaDomain":
		return "Messages attachments", "Settings › General › Storage › Messages › Review Large Attachments"
	case domain == "HomeDomain":
		return "System and iCloud Drive files", "Files › iCloud Drive: long press › Remove Download"
	}
	id := domain
	for _, p := range []string{"AppDomainGroup-group.", "AppDomainGroup-", "AppDomainPlugin-", "AppDomain-"} {
		id = strings.TrimPrefix(id, p)
	}
	if n, ok := apps[id]; ok {
		return n, "inside the app, or Settings › General › Storage › " + n
	}
	for appID, n := range apps { // a group id like com.google.Keep for the app com.google.Keep
		if strings.HasPrefix(id, appID) || strings.HasPrefix(appID, id) {
			return n, "inside the app, or Settings › General › Storage › " + n
		}
	}
	return id, ""
}

// MakeStorageReport reads the device's local backup in dir (…/iPhoneBackup/<udid>).
func MakeStorageReport(run Runner, dir, tmp string, apps map[string]string) (StorageReport, error) {
	r := StorageReport{Time: time.Now().Format(time.RFC3339), Kinds: map[string]int64{}}
	man := filepath.Join(dir, "Manifest.db")
	st, err := os.Stat(man)
	if err != nil {
		return r, errors.New("no local backup of this device yet: make one in the Backup tab")
	}
	r.BackupTime = st.ModTime().Format(time.RFC3339)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return r, err
	}
	defer os.RemoveAll(tmp)
	cp := filepath.Join(tmp, "Manifest.db")
	if err := copyFile(man, cp, time.Time{}); err != nil {
		return r, err
	}
	var rows []struct {
		ID     string `json:"fileID"`
		Domain string `json:"domain"`
		Rel    string `json:"relativePath"`
	}
	if err := sqliteJSON(run, cp, "SELECT fileID, domain, relativePath FROM Files WHERE flags = 1;", &rows); err != nil {
		return r, errors.New("the backup cannot be read (encrypted backups cannot be analysed)")
	}
	blob := func(id string) string { return filepath.Join(dir, id[:min(2, len(id))], id) }
	areas := map[string]*StorageArea{}
	chats := map[string]*ChatMedia{}
	type media struct {
		jid, ext string
		size     int64
		rel      string
	}
	var waMedia []media
	chatDB := ""
	for _, f := range rows {
		if len(f.ID) < 2 {
			continue
		}
		fi, err := os.Stat(blob(f.ID))
		if err != nil {
			continue
		}
		a := areas[f.Domain]
		if a == nil {
			a = &StorageArea{Domain: f.Domain}
			areas[f.Domain] = a
		}
		a.Bytes += fi.Size()
		a.Files++
		r.Total += fi.Size()
		if f.Domain != waDomain {
			continue
		}
		if f.Rel == "ChatStorage.sqlite" {
			chatDB = blob(f.ID)
		}
		if parts := strings.Split(f.Rel, "/"); len(parts) > 3 && parts[0] == "Message" && parts[1] == "Media" {
			ext := strings.ToLower(path.Ext(f.Rel))
			waMedia = append(waMedia, media{jid: parts[2], ext: ext, size: fi.Size(), rel: strings.TrimPrefix(f.Rel, "Message/")})
		}
	}
	for _, a := range areas {
		a.Name, a.Hint = areaName(a.Domain, apps)
		r.Areas = append(r.Areas, *a)
	}
	sort.Slice(r.Areas, func(i, j int) bool { return r.Areas[i].Bytes > r.Areas[j].Bytes })
	if len(r.Areas) > 12 {
		r.Areas = r.Areas[:12]
	}
	if wa := areas[waDomain]; wa != nil {
		r.WhatsApp = wa.Bytes
	}
	// WhatsApp: chat names and message dates (no message text)
	names := map[string]string{}
	dates := map[string]string{}
	if chatDB != "" {
		wdb := filepath.Join(tmp, "ChatStorage.sqlite")
		if copyFile(chatDB, wdb, time.Time{}) == nil {
			var cs []struct {
				JID  string `json:"j"`
				Name string `json:"n"`
			}
			_ = sqliteJSON(run, wdb, "SELECT ZCONTACTJID AS j, ZPARTNERNAME AS n FROM ZWACHATSESSION;", &cs)
			for _, c := range cs {
				names[c.JID] = c.Name
			}
			var ds []struct {
				P string `json:"p"`
				D string `json:"d"`
			}
			_ = sqliteJSON(run, wdb, "SELECT m.ZMEDIALOCALPATH AS p, date(g.ZMESSAGEDATE + 978307200, 'unixepoch', 'localtime') AS d "+
				"FROM ZWAMEDIAITEM m JOIN ZWAMESSAGE g ON m.ZMESSAGE = g.Z_PK WHERE m.ZFILESIZE > 5000000;", &ds)
			for _, d := range ds {
				dates[d.P] = d.D
			}
		}
	}
	chatName := func(jid string) string {
		if n := names[jid]; n != "" {
			return n
		}
		return strings.SplitN(jid, "@", 2)[0]
	}
	for _, m := range waMedia {
		c := chats[m.jid]
		if c == nil {
			c = &ChatMedia{Name: chatName(m.jid), Group: strings.HasSuffix(m.jid, "@g.us")}
			chats[m.jid] = c
		}
		c.Bytes += m.size
		c.Files++
		r.Kinds[kindOf(m.ext)] += m.size
	}
	for _, c := range chats {
		r.Chats = append(r.Chats, *c)
	}
	sort.Slice(r.Chats, func(i, j int) bool { return r.Chats[i].Bytes > r.Chats[j].Bytes })
	if len(r.Chats) > 12 {
		r.Chats = r.Chats[:12]
	}
	sort.Slice(waMedia, func(i, j int) bool { return waMedia[i].size > waMedia[j].size })
	for _, m := range waMedia[:min(20, len(waMedia))] {
		r.Heavy = append(r.Heavy, HeavyAttachment{Chat: chatName(m.jid), Kind: kindOf(m.ext), Ext: strings.TrimPrefix(m.ext, "."), Bytes: m.size, Date: dates[m.rel]})
	}
	return r, nil
}

// Storage makes the report of one device from its local backup and saves it for the widget.
func Storage(run Runner, p Paths, udid string) (StorageReport, error) {
	cfg := p.LoadConfig()
	apps := map[string]string{}
	var as struct {
		Apps []AppSize `json:"apps"`
	}
	if readJSON(p.devFile(udid, "apps.json"), &as) {
		for _, a := range as.Apps {
			apps[a.ID] = a.Name
		}
	}
	r, err := MakeStorageReport(run, filepath.Join(cfg.BackupDir, udid), filepath.Join(p.Runtime, "storage-"+udid), apps)
	if err != nil {
		return r, err
	}
	return r, writeJSON(p.devFile(udid, "storage.json"), r)
}

// StorageStatus: the last report of one device.
func StorageStatus(p Paths, udid string) any {
	var r StorageReport
	if !readJSON(p.devFile(udid, "storage.json"), &r) {
		return nil
	}
	return r
}
