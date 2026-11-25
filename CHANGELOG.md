# Changelog

What changed in each version of Driveway, for people who use it. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow the app's own numbering.
Versions 0.1.0 to 0.2.13 were private test builds and were not published.

## [Unreleased]

## [0.3.0] - 2026-11-25

The first public release.

### Added
- Settings, About: **Check for updates** asks GitHub for the latest release and opens its page
  when there is a newer one. **Check weekly**, off until you turn it on, does the same about once
  a week and tells you in a notification. Nothing about you or your device is sent, and Driveway
  never downloads or installs anything itself.
- About now lists Merriment Labs, the source code and the license, in the same order as the other
  Merriment Labs apps.

### Changed
- The SFTP form's Server hint matches the other forms ("nas.local or 192.168.1.10").
- A server that can't sign in again offers Remove on its card.
- The Server field of SFTP, SMB, NFS and FTP accepts a port after the name, like
  `nas.local:2222` or `[2001:db8::1]:2222`: as soon as you leave the field, the port moves to Port
  under More settings, and wins over a different one already there (the form says so). A port that isn't a number from 1 to
  65535, a folder, a user name, or another kind of server's address (`https://...`) is pointed
  out on the form instead of failing as "can't reach".
- The Address field of WebDAV moves a user name and password typed in it
  (`https://you:password@host/dav`) to User name and Password as soon as you leave it, and says
  so. An address Driveway can't use, such as one without `https://` or with a password that holds
  a space, a quote or a slash, is pointed out on the form, without repeating it, instead of
  failing as "can't reach".
- An error says what happened and what to do next, in Driveway's own words, on a server's card,
  in Files and in a failed save's notification. The technical detail from the server or the
  network goes only to the app's log, with passwords, API keys, private keys and sign-in tokens
  found by their shape and taken out, and each line cut to 8,000 characters. A
  server that can't be reached names what to check: its address and port when you typed one, the
  internet connection for a service such as Dropbox. A sign-in a service refuses shows what the
  service said, labeled as the service's.

## [0.2.13] - 2026-11-18

### Security
- A sign-in refuses to start when another app on the device holds the IPv6 loopback port the
  sign-in comes back to, and says why, for Driveway's own sign-ins and rclone's alike. Before, a
  browser that delivered the answer to `[::1]` could have handed it to that other app.

## [0.2.12] - 2026-11-15

### Changed
- Servers are compact rows: a tile, the name, and a second line only when it adds something.
- Box, Dropbox and OneDrive show letter tiles in Driveway's colors; no company's logo ships.
- How a sign-in is kept on the device is a note at the foot of each form.

## [0.2.11] - 2026-11-15

### Changed
- The cloud services in Add a server are listed A to Z; the main button leads each form; Start in
  folder moves under More settings; one type scale across the app.

## [0.2.10] - 2026-11-15

### Changed
- On a large screen, Files opens beside a floating Driveway window, at Driveway's first server.

## [0.2.9] - 2026-11-15

### Changed
- Two columns of servers from a 600dp window; Files opens beside Driveway on large screens.

## [0.2.8] - 2026-11-14

### Added
- Box signs in with one tap (in builds that carry Merriment Labs' Box registration).
- A "Return to Driveway" button on the browser page a sign-in finishes on.

### Fixed
- OneDrive opens the account's own drive instead of a hidden system drive.

## [0.2.7] - 2026-11-06

### Changed
- Every Allow button shows Android's own dialog while it still asks, and App info only once a
  permission was refused for good.

### Fixed
- A save resumed after a restart no longer holds back an unrelated move's original.

## [0.2.6] - 2026-11-06

### Fixed
- Delete, rename, move and copy in Files fit inside the time Files allows, including any wait for
  a save to finish.

## [0.2.5] - 2026-11-06

### Changed
- While Save or Test connection runs, the form is locked, so an edit made meanwhile isn't lost.
- A sign-in that ended says so on the form; a service with no other way in says it can't sign
  in again instead of offering a button that can't help.

### Fixed
- A service that refuses to refresh a sign-in is reported as a sign-in problem.

## [0.2.4] - 2026-10-04

### Added
- A "New" label on services an update made available.

### Changed
- Services with nothing to set up are hidden instead of listed without a way to add them.
- A sign-in left open for ten minutes says so.

## [0.2.3] - 2026-10-04

### Added
- Dropbox and OneDrive sign in with Merriment Labs' own registrations (PKCE, no secret in the app).

### Removed
- Google Photos, ShareFile, and the rclone services that only layer on top of another server.
  Servers of these added before say so on their card and can be removed.

## [0.2.2] - 2026-10-03

### Added
- Google Drive with a service account key saves to a shared drive, or opens a shared folder
  read-only.

## [0.2.1] - 2026-10-03

### Changed
- Services that would borrow rclone's app registration to sign in can no longer be added that way.

## [0.2.0] - 2026-10-03

### Added
- Driveway's own screens: a three-step Add a server with a live connection checklist, Settings,
  and an open-source licenses screen.

## [0.1.0] - 2026-10-02

### Added
- First version: SFTP, SMB, NFS, WebDAV and rclone's other services as roots in Files, through a
  documents provider over rclone compiled in with gomobile.
