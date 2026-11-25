// SPDX-License-Identifier: Apache-2.0

package engine

// rclone's services compiled into Driveway: every one in rclone's
// backend/all except those removed by decision (removedBackends), so they
// aren't in the app at all. TestCompiledBackends compares this list with
// rclone's after each upgrade.
import (
	"github.com/rclone/rclone/fs"

	_ "github.com/rclone/rclone/backend/azureblob"
	_ "github.com/rclone/rclone/backend/azurefiles"
	_ "github.com/rclone/rclone/backend/b2"
	_ "github.com/rclone/rclone/backend/box"
	_ "github.com/rclone/rclone/backend/cloudinary"
	_ "github.com/rclone/rclone/backend/doi"
	_ "github.com/rclone/rclone/backend/drime"
	_ "github.com/rclone/rclone/backend/drive"
	_ "github.com/rclone/rclone/backend/dropbox"
	_ "github.com/rclone/rclone/backend/fichier"
	_ "github.com/rclone/rclone/backend/filefabric"
	_ "github.com/rclone/rclone/backend/filelu"
	_ "github.com/rclone/rclone/backend/filen"
	_ "github.com/rclone/rclone/backend/filescom"
	_ "github.com/rclone/rclone/backend/ftp"
	_ "github.com/rclone/rclone/backend/gofile"
	_ "github.com/rclone/rclone/backend/googlecloudstorage"
	_ "github.com/rclone/rclone/backend/hdfs"
	_ "github.com/rclone/rclone/backend/hidrive"
	_ "github.com/rclone/rclone/backend/http"
	_ "github.com/rclone/rclone/backend/huaweidrive"
	_ "github.com/rclone/rclone/backend/iclouddrive"
	_ "github.com/rclone/rclone/backend/imagekit"
	_ "github.com/rclone/rclone/backend/internetarchive"
	_ "github.com/rclone/rclone/backend/internxt"
	_ "github.com/rclone/rclone/backend/jottacloud"
	_ "github.com/rclone/rclone/backend/koofr"
	_ "github.com/rclone/rclone/backend/linkbox"
	_ "github.com/rclone/rclone/backend/local"
	_ "github.com/rclone/rclone/backend/mailru"
	_ "github.com/rclone/rclone/backend/mega"
	_ "github.com/rclone/rclone/backend/memory"
	_ "github.com/rclone/rclone/backend/netstorage"
	_ "github.com/rclone/rclone/backend/onedrive"
	_ "github.com/rclone/rclone/backend/opendrive"
	_ "github.com/rclone/rclone/backend/oracleobjectstorage"
	_ "github.com/rclone/rclone/backend/pcloud"
	_ "github.com/rclone/rclone/backend/pikpak"
	_ "github.com/rclone/rclone/backend/pixeldrain"
	_ "github.com/rclone/rclone/backend/premiumizeme"
	_ "github.com/rclone/rclone/backend/protondrive"
	_ "github.com/rclone/rclone/backend/putio"
	_ "github.com/rclone/rclone/backend/qingstor"
	_ "github.com/rclone/rclone/backend/quatrix"
	_ "github.com/rclone/rclone/backend/s3"
	_ "github.com/rclone/rclone/backend/seafile"
	_ "github.com/rclone/rclone/backend/sftp"
	_ "github.com/rclone/rclone/backend/shade"
	_ "github.com/rclone/rclone/backend/sia"
	_ "github.com/rclone/rclone/backend/smb"
	_ "github.com/rclone/rclone/backend/storj"
	_ "github.com/rclone/rclone/backend/sugarsync"
	_ "github.com/rclone/rclone/backend/swift"
	_ "github.com/rclone/rclone/backend/ulozto"
	_ "github.com/rclone/rclone/backend/webdav"
	_ "github.com/rclone/rclone/backend/yandex"
	_ "github.com/rclone/rclone/backend/zoho"
)

// removedBackends are rclone services left out of Driveway on purpose, so
// they aren't in the app at all: Google Photos,
// whose API now shows an app only the photos it uploaded itself; ShareFile,
// which needs a paid account and an https address to sign in through; and
// the services that only layer on top of another server (crypt, union,
// chunker, combine, compress, hasher, archive, alias, cache), whose result
// could only be read through Driveway. Keys are rclone's package names,
// values its service names.
var removedBackends = map[string]string{
	"googlephotos": "google photos", "sharefile": "sharefile",
	"alias": "alias", "archive": "archive", "cache": "cache", "chunker": "chunker", "combine": "combine",
	"compress": "compress", "crypt": "crypt", "hasher": "hasher", "union": "union",
}

// rclone's own fs/operations imports crypt, so its code stays in the binary;
// taking every removed service out of rclone's registry makes it unknown to
// rclone as well as to the app, like the ones left out of the build.
func init() {
	kept := fs.Registry[:0]
	for _, ri := range fs.Registry {
		removed := false
		for _, name := range removedBackends {
			if ri.Name == name {
				removed = true
			}
		}
		if !removed {
			kept = append(kept, ri)
		}
	}
	fs.Registry = kept
}
