# Service brand marks

**No third-party logo ships.** Box, Dropbox and OneDrive show a letter tile in Driveway's own colors
(B, D, O: the theme's tonal container with its on-container ink, the same 40dp tile and 12dp corners as
every service glyph; `Ui.serviceTile`, `Kinds.letter`). Every other service keeps its Material Symbols
glyph. The services are named in text only, which all three owners allow for saying an app works with
their service. THIRD_PARTY_NOTICES.md carries the trademark line (written by `tools/licenses.py`).

## Why

What each owner allows an app to do with its logo (read November 2026):

| Service | Logo use by an app | Source |
|---|---|---|
| Dropbox | Allowed only to identify or direct to a Dropbox integration, approved files only, unmodified | Developer branding guide, https://docs.dropboxapi.com/dropbox-api/docs/developer-resources/branding-guide; https://www.dropbox.com/branding; the Brand partner toolkit linked from https://brand.dropbox.com/logo |
| OneDrive | Needs a Microsoft license: "Many uses, including our logos, app and product icons, and other designs, will require a license first"; "Don't use Microsoft's logos, icons, or designs, in any manner". Text may say an app is compatible | https://www.microsoft.com/en-us/legal/intellectualproperty/trademarks |
| Box | Needs an agreement or express written authorization: "third parties generally may not use any Box logo for any purpose" (rule 11) | https://www.box.com/legal/trademark |

So at most one of the three marks could ship, and a row with one blue Dropbox glyph between two
generic clouds reads unevenly. Neutral letter tiles for all three need no permission from anyone and
stay correct in the dark theme. Showing a real mark would take a written authorization from Box, a
license from Microsoft, or Dropbox's developer-guide terms.
