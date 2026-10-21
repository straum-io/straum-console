# Fonts

The docs site's fonts (straum-io.github.io `astro.config.mjs`), embedded so
the console needs no network access and keeps its CSP (CON-D-5 A). Each is
the Latin subset Google Fonts serves, as one variable woff2 file, fetched
on 2026-10-21. All three are under the SIL Open Font License 1.1, with no
Reserved Font Name; each licence file is the family's `OFL.txt` from
github.com/google/fonts.

| Family | File | Licence | Source |
|---|---|---|---|
| Inter | `inter-v20.woff2` | `inter-OFL.txt` | https://fonts.gstatic.com/s/inter/v20/UcC73FwrK3iLTeHuS_nVMrMxCp50SjIa1ZL7W0Q5nw.woff2 |
| Inter Tight | `inter-tight-v9.woff2` | `inter-tight-OFL.txt` | https://fonts.gstatic.com/s/intertight/v9/NGSwv5HMAFg6IuGlBNMjxLsH8ahuQ2e8.woff2 |
| JetBrains Mono | `jetbrains-mono-v24.woff2` | `jetbrains-mono-OFL.txt` | https://fonts.gstatic.com/s/jetbrainsmono/v24/tDbv2o-flEEny0FZhsfKu5WU4zr3E_BX0PnT8RD8yKwBNntkaToggR7BYRbKPxDcwgknk-4.woff2 |

The `v` in a file name is Google Fonts' version of the family, so a
browser never keeps an old file under a new name. To update, fetch the new
files from the same CSS (`fonts.googleapis.com/css2` with the families and
weights of `astro.config.mjs`) and rename them with the new version.
