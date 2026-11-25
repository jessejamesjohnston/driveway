# Large screens and accessibility

Driveway runs on phones, tablets and Googlebooks, with touch, a keyboard, a mouse or a screen
reader. This page says what its screens do for each, and how that is checked. The screens are
hand-built Java views, so each behavior below names the class that provides it.

## Window size and layout

- **Width decided by the space actually there.** Compact under 600dp, medium 600 to 839dp,
  expanded from 840dp. Driveway's activities keep their views across window resizes
  (`configChanges`, so a half-filled form or a sign-in survives a desktop window being dragged),
  so every width decision is made at measure time in `Layouts.java`, never once at startup:
  - `Flow`: buttons in a row wrap onto more lines when they don't fit.
  - `Split`: a row's text with its buttons at the end; the buttons move below the text when the
    text would get under 220dp (scaled up with the font size).
  - `Grid`: the server cards sit in two columns in an expanded window only (each column at least
    390dp, so from about 840dp of window; wider at large text), one column on phones and medium
    windows. The kind tiles in Add a server: as many as fit at 150dp, up to four.
  - `Columns`: a form's Server and Sign in groups side by side in an expanded window only (840dp
    and wider, the window's own width from `WindowMetrics`), stacked below that.
- **A readable width on wide windows.** Pages center a column of at most 720dp (the home screen
  1040dp, for its two columns of cards). Side margins are 16dp in a compact window and 24dp
  otherwise.
- **Two panes only where they help.** The home screen's two columns of cards, yes. A
  list-and-detail layout (a server's form beside the list) is deliberately left out: the cards
  already show each server's status, problem and Open in Files, so a detail panel would repeat
  them, and the form keeps its own screen with its sign-in flow and keyboard handling. Servers
  found on the network are one line above the cards at every width.
- **The main action is the biggest control.** A form's main button (Connect, Save, Sign in) is a
  full-width filled button right under the settings it needs, with Sign in again, Test connection
  and Cancel as smaller buttons below it. A new server's connection runs on its own screen, with
  Back to details (which stops it) in a bar under the scrolling page.
- **Edge to edge.** Each page's outer frame is padded by the status and navigation bars, a
  desktop window's caption, display cutouts and the on-screen keyboard (`Ui.page`), so scrolled
  text stops at the bars, and when the keyboard opens the page gets shorter and keeps the field
  being typed in above it.
- **Resizing keeps state.** Resizing a window doesn't rebuild a screen. A change of dark theme,
  text size or display density does: the server form, and a connection in progress, are handed
  to the rebuilt screen in memory (`ServerActivity.Kept`, via `onRetainNonConfigurationInstance`),
  never written into the saved-state Bundle, so passwords and a new key stay off disk. A form in
  an app that Android stopped in the background starts over.
- **Small windows work.** Minimum window 320 x 320dp (desktop windowing and split screen); every
  page scrolls.
- **Desktop windowing quirk:** Settings opens in its own window and Driveway stays resumed, so
  `onResume` doesn't run on the way back. The main screen checks the Nearby devices permission
  again on `onTopResumedActivityChanged(true)` and `onWindowFocusChanged(true)`.

## Keyboard

- **Tab** reaches every control in reading order: on a server card, the card (Enter opens it in
  Files), its ⋮ button, then its problem's buttons; the rows of the kind and service lists and the
  licenses list. The Menu key or Shift+F10 on a card opens its menu, as a long press or
  right-click does.
- **A visible focus ring:** a 3dp ring in the accent color (`Ui.focusRing`) on buttons and rows.
  The page itself never takes focus, so Android's default whole-page highlight never shows.
- **Enter** moves to the next field of a form; in the last one it connects or saves. In a setup
  question it answers Continue. Dialogs give focus to their safe button when the choice is
  destructive or a matter of trust (Remove, Save anyway, a server's identity), so Enter never
  removes or trusts by accident.
- **Escape** leaves a form like Back: if anything was changed it asks "Discard your changes to
  this server?" first, with Keep editing focused. Escape closes dialogs and the About page. The
  Cancel button discards without asking (it says what it does).
- **Shortcuts**, only where natural: Ctrl+N adds a server and Ctrl+comma opens Settings (home),
  Ctrl+S connects or saves (server form). All are listed in the system's shortcut list (Meta+/).
- **Focus stays put when a form locks.** While Save or Test connection runs, the form's fields
  and buttons are disabled; if one had the keyboard's (or a screen reader's) focus, the
  checklist's line for the step being run takes it, with the focus ring, so it never jumps to the
  top of the page. A question from the run then takes it.
- **Trust is never the default.** When the checklist asks about an SFTP server's identity, Cancel
  takes the keyboard ("Cancel, don't trust 10.0.2.2"); Trust and continue must be chosen.

## Screen readers (TalkBack)

- Every control has a label, and no two controls on a screen share one (`tools/a11y-check.py`
  fails a screen where they do). Buttons that repeat on each card say what they act on, starting
  with the visible word so voice commands still match it: "Open Family NAS in Files", "More for
  Family NAS", "Try again: report.pdf on Family NAS", "Sign in again to Dropbox", "Add Office NAS
  (SMB)", "Copy the key line".
- A server card is one stop with its name and address ("Open Family NAS in Files. SMB ·
  nas.local").
- Each checklist line is read with its state ("Sign in as sam, not yet", "Found nas.local, done")
  and announced when it changes. A service labeled New is read as "<name>, new" with its
  subtitle; the chip itself is skipped.
- Screen titles and section headings are marked as headings, and window titles name each screen
  (Add a server, Change server, About Driveway).
- Each field's label is tied to it (`setLabelFor`); a long hint shown below its field is still
  read out as the field's hint. A required field left empty takes focus and shows its error.
- Changes are announced: the form's status line (Connecting…, Connected, sign-in steps, errors),
  "Looking for file servers…" and its result, and the Nearby devices notice are polite live
  regions. "More settings" reports Shown or Hidden.
- Icons are decorative (the words beside them carry the meaning) and skipped by screen readers;
  icon-only buttons (Back, Settings, ⋮) are labeled and show a tooltip. Notifications carry their
  state as text ("45% · 120 MB of 268 MB") and their actions are labeled (Try again, Discard).

## Touch targets and text size

- Every control is at least 48 x 48dp (buttons, fields, choice lists, rows, the SFTP key line).
- Text is in sp and wraps; nothing is fixed-height. At 200% font size rows stack their buttons
  below the text (`Split`), button rows wrap (`Flow`), and field hints longer than 24 characters
  move below the field instead of being cut off inside it.
- With animations off in system settings, the finish screen's car is parked from the start and a
  new server's card appears without sliding.

## Contrast and themes

- Light and dark themes follow the system. Driveway sets its own colors (`res/values*/colors.xml`)
  instead of the wallpaper palette, so the figures below hold on every device. The accent is
  Driveway's green, `#2F6F5E`, lightened to `#8FD1BC` in the dark theme.
- A state is never shown by color alone: a word always carries it ("didn't reach", "has ended").
  A form locked while a connection runs says so in its checklist's words ("Looking for
  nas.local…"), and screen readers read each field as disabled.
- Contrast, computed from the color tokens (the rendered pixels match them exactly):

  | | Light | Dark |
  |---|---|---|
  | Text on a card / the page | 16.3:1 / 14.9:1 | 14.1:1 / 15.6:1 |
  | Muted text on a card / the page | 6.8:1 / 6.2:1 | 8.1:1 / 8.9:1 |
  | Accent text on a card / the page | 5.8:1 / 5.3:1 | 9.5:1 / 10.6:1 |
  | Text on the filled button | 5.9:1 | 8.8:1 |
  | Tonal button text | 7.8:1 | 8.9:1 |
  | A save waiting (amber strip) | 7.4:1 | 8.3:1 |
  | Sign-in ended, identity changed (red strip) | 7.0:1 | 8.3:1 |
  | A field's outline on a card | 4.0:1 | 5.6:1 |
  | A disabled field's text (ink at 60% on the page color) | 4.3:1 | 6.2:1 |
  | A disabled field's outline (ink at 20%) / its fill against the card | 1.5:1 / 1.1:1 | 1.8:1 / 1.1:1 |

  Disabled controls have no contrast minimum (WCAG 1.4.3 and 1.4.11 exempt inactive components):
  the outline drops well below the 3:1 of an enabled field and the field sinks to the page color,
  so it reads as off, while its text stays readable.

## Right to left, mouse and trackpad

- `android:supportsRtl="true"`; the custom layouts mirror themselves (cards, tiles, rows, the Back
  arrow and chevrons); text and buttons start at the start edge, not the left.
- Right-click (or long press, or the Menu key) on a server's card opens its menu: Open in Files,
  Change, Remove, the same as its ⋮ button. Right-click goes through Android's context-click
  callback. Rows and buttons highlight on hover.

## How it is checked

`tools/a11y-check.py` dumps the current screen with uiautomator and fails if any clickable control
has no text or description (counting text in its children, as a screen reader does), if two
controls share a label, or if any clickable or focusable control is under 48 x 48dp at the
device's real density. A row cut off at the edge of its scrolling page is skipped, so long pages
are scrolled and checked screenful by screenful.

`tools/screens.sh` walks 26 screens with made-up servers against the test servers (home, empty
home, Where are your files, More services, the SFTP, OneDrive and S3 forms, the identity
question, a failure with its fix, the finish, a server's form before, during and after Test
connection, forms for an old or hidden sign-in, Settings, the licenses and a license's text), runs the check on each and
saves a screenshot. It runs at the phone size (1080 x 2400 at 420dpi), the tablet size (2560 x
1600) and in a 1920 x 1080 desktop window, in the light and dark themes and at 200% text.

By hand on an emulator, after a change to a screen:

1. Keyboard: `adb shell input keyevent TAB`, `ENTER`, `ESCAPE` through a form and its dialogs;
   Ctrl+N and Ctrl+S.
2. A form filled in (with a new SFTP key and More settings open), then the theme switched, and
   again with a connection waiting at the identity question: everything is still there.
3. Right to left: `adb shell settings put global debug.force_rtl 1`, then restart Driveway.
4. Columns: the form's groups stacked at 839dp and side by side at 840dp; the home screen's cards
   in one and two columns.

Current results: 0 problems on all 26 screens at the phone size, in the light theme, the dark
theme and at 200% text. The last walk at the tablet size covered the 13 screens that existed
then, and the last desktop-window check covered home, Where are your files, a form and Settings;
both gave 0. The keyboard, theme-change, right-to-left and
column checks behave as this page describes.

Not covered: TalkBack speech itself (TalkBack isn't on the emulator images, so speech is checked
through the accessibility tree, not by listening) and a real mouse's right button (the emulator
has none; long press and the Menu key are checked). Driveway has also been used by hand on a
Googlebook.
