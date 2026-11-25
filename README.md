# fanbox-dl: Pixiv FANBOX Downloader

`fanbox-dl` will download media of supported and followed creators on FANBOX.

This project has been enhanced with features from [PixivFanboxDownloader](https://github.com/konnokai/PixivFanboxDownloader), including:
- HTML page generation
- Gigafile auto-download
- Google Drive auto-download
- State management for tracking downloads

Caution: `fanbox-dl` is command-line-program, so it doesn't provide graphical user interface.

## Installation

The latest binary for this merged fork can be downloaded [here](https://github.com/ciallo-bird/fanbox-dl/releases/latest).

- Windows (64-bit): `fanbox-dl_Windows_x86_64.zip`
- macOS (Intel): `fanbox-dl_Darwin_x86_64.tar.gz`
- macOS (Apple Silicon): `fanbox-dl_Darwin_arm64.tar.gz`
- Linux: `fanbox-dl_Linux_x86_64.tar.gz` or `fanbox-dl_Linux_arm64.tar.gz`

Extract the archive before running the binary. Each release includes `checksums.txt` for SHA-256 verification.

## Usage

1. Open a command line interpreter. For example, If you are Windows user, open `Command Prompt` or `PowerShell`. If you are Mac user, open `Terminal`.
2. Execute downloaded `fanbox-dl` binary. You can see usage by running `fanbox-dl --help`.

> [!NOTE]
>
> `--sessid` and `--cookie` can not be used together; when both are used, `--cookie` will be used.

| Command | Description | Usage | Default |
| --- | --- | --- | --- |
| sessid | Requires FANBOXSESSID which is stored in browser Cookies for login state. <br>When not provided, refers FANBOXSESSID environment value. <br>If unavailable, only free posts are downloaded when accompanied by a `creator` flag. | `--sessid xxxxx` | `NULL` |
| cookie | Full Cookie string for the FANBOX API. Overrides `sessid`; `FANBOX_COOKIE` is used when neither a flag nor `FANBOXSESSID` is set. | `--cookie "name=value; name2=value2"` | `NULL` |
| creator | Comma separated Pixiv creator IDs to download contents. <br>Overrides `supporting` and `following` flags. <br>`https://www.fanbox.cc/@`**example**. <br>Only bold text needed from URL. | `--creator user1`, `--creator user1,user2` | `NULL` |
| ignore-creator | Comma separated Pixiv creator IDs to ignore to download contents. | `--ignore-creator user1,user2` | `NULL` |
| supporting | When disabled, will not download content from creators you're supporting. | `--supporting=false` | `true` |
| following | When disabled, will not download content from creators you only follow. | `--following=false` | `true` |
| dir-by-plan | Separates content saved into directories based on the plan that post belonged to. | `--dir-by-plan` | `false` |
| dir-by-post | Separates content saved into directories based on title of post. <br>Stored inside plan directory when accompanied by `dir-by-plan` flag. | `--dir-by-post` | `false` |
| all | Will ensure that all content is downloaded from creators. <br>Will also redownload content that might already be present locally. | `--all` | `false` |
| skip-files | Will skip downloading non-image files from creators. | `--skip-files` | `false` |
| skip-images | Will skip downloading images from creators. This is useful when you only want to download files. | `--skip-images` | `false` |
| skip-texts | Will skip downloading post contents as text files. | `--skip-texts` | `false` |
| skip-on-error | Will skip downloading instead of exiting when an error occurs. | `--skip-on-error` | `false` |
| dry-run | Will skip downloading all content from creators. | `--dry-run` | `false` |
| verbose | Gives more detailed information about commands being executed by application. <br>Useful for debugging errors. | `--verbose` | `false` |
| save-dir | Root directory to save content. <br>Put directory in double quotes `"` if it contains spaces. <br>Supports relative and absolute directories. | `--save-dir ./content` | `./images` |
| user-agent | User agent for requests. Keep it aligned with the Chrome 146 PSK TLS profile; arbitrary overrides can trigger anti-bot checks. | `--user-agent "Mozilla/5.0 ... Chrome/146.0.0.0 Safari/537.36"` | Chrome 146 |
| remove-unprintable-chars | Removes unprintable characters from file name. In some environments, unprintable characters are not allowed in file names. | `--remove-unprintable-chars` | `false` |
| rate-limit | Maximum FANBOX request rate, including retries and media requests. Set to 0 to explicitly disable pacing. | `--rate-limit 0.5` | `0.5` |
| save-json | Save original API JSON responses as `post.json` files in each post directory. | `--save-json` | `false` |
| start-date | Only download posts published on or after this date. Format: YYYY-MM-DD | `--start-date 2023-01-01` | `NULL` |
| end-date | Only download posts published on or before this date. Format: YYYY-MM-DD | `--end-date 2023-12-31` | `NULL` |
| save-html | Generate HTML pages for downloaded posts. | `--save-html` | `false` |
| html-language | Language for HTML generation (zh-CN, zh-TW, ja, en). | `--html-language ja` | `zh-CN` |
| download-gigafiles | Automatically download files from gigafile.nu links in post content. | `--download-gigafiles` | `false` |
| download-drive | Automatically download files from Google Drive links in post content. | `--download-drive` | `false` |
| download-threads | Number of concurrent download workers for assets and external links. | `--download-threads 4` | `1` |
| timeout | HTTP timeout for downloads and API requests (e.g., 30s, 5m). | `--timeout 60s` | `2m` |
| use-state-manager | Use state manager to track downloaded posts (creates LastSavePostId.json). | `--use-state-manager` | `false` |
| check | Check downloaded content and repair missing images, files, text, JSON and HTML. Waits for repair work before exiting. | `--check` | `false` |

### Example

If you want to re-download all images from creator `https://www.fanbox.cc/@creatornamehere`, execute `fanbox-dl --sessid xxxxx --save-dir ./content --creator creatornamehere --all`.

And you can see media in the relevant directory. `./content/creatornamehere/xxxx.jpg`.

### Acquiring your FANBOXSESSID

fanbox-dl needs your account FANBOXSESSID to download supported content, which has your login state stored in a browser Cookie.

For example, if you are using Google Chrome, you can get it by following to steps in https://developers.google.com/web/tools/chrome-devtools/storage/cookies.

### Session safety

Use the same network/IP as your logged-in browser and avoid simultaneous crawlers using the same session. Leave the default `--rate-limit 0.5` and `--download-threads 1` in place unless you understand the account-risk tradeoff. Do not repeatedly retry after a rejection or switch proxies to force access.

Imported login cookies are scoped to HTTPS `api.fanbox.cc`, not media CDNs or external download hosts. Server-issued Cloudflare cookies and session rotations are retained in memory, including across retries; the original cookie string is not replayed over newer values. Cookie state is not written back to your browser or stored on disk.

HTTP 401/403, API login redirects, `authentication_required` responses, and deletion/expiry of `FANBOXSESSID` stop subsequent FANBOX requests. `--skip-on-error` does not bypass this protection. Check the browser login and network before starting a new run. Empty paid-plan lists are not treated as proof that a session is invalid.

Prefer the `FANBOXSESSID` environment variable over putting a session ID in command-line arguments or shell history. `FANBOX_COOKIE` accepts a full cookie string; precedence is `--cookie`, `--sessid`, `FANBOXSESSID`, then `FANBOX_COOKIE`. Never share cookies in logs or bug reports.

These safeguards reduce known client-side risks; they cannot guarantee that FANBOX will never revoke a session. Offline tests and a local HTTPS transport test do not replace authenticated live-account verification.

### Advanced Features

#### HTML Page Generation

Generate HTML pages for downloaded posts, similar to the original FANBOX website but saved locally:

```bash
fanbox-dl --cookie "YOUR_COOKIE" --save-html
```

This creates an HTML file with:
- Formatted post title, metadata, and content
- Embedded images and hyperlinks to downloaded files
- Responsive design with CSS styling
- Compatible with `--dir-by-post` mode

#### Gigafile Auto-Download

Automatically detect and download files from gigafile.nu links found in post content:

```bash
fanbox-dl --cookie "YOUR_COOKIE" --save-html --download-gigafiles
```

This feature:
- Scans HTML content for gigafile.nu URLs
- Automatically downloads files to the same directory
- Handles cookies and filenames from the gigafile server

#### Google Drive Auto-Download

Automatically detect and download files from Google Drive links found in post content:

```bash
fanbox-dl --cookie "YOUR_COOKIE" --save-html --download-drive
```

This feature:
- Scans HTML content for Google Drive URLs (e.g., `https://drive.google.com/file/d/FILE_ID/view?usp=sharing`)
- Automatically downloads files to the same directory
- Handles large files and virus scan warning pages
- Extracts filenames from Content-Disposition headers
- Supports resume from interrupted downloads via `.part` files
- Compatible with [gdown](https://github.com/wkentaro/gdown) download strategies

**Optional: Using Google Drive API for faster downloads**

For better performance and reliability, you can configure a Google Drive API key:

```bash
# Get a free API key from https://console.cloud.google.com/apis/credentials
# 1. Create a new project or select existing one
# 2. Enable Google Drive API
# 3. Create credentials > API key
# 4. Set the API key as environment variable

export GOOGLE_DRIVE_API_KEY="your-api-key-here"

# Run fanbox-dl
fanbox-dl --cookie "YOUR_COOKIE" --download-drive
```

When API key is configured, the downloader will:
- First attempt to download via Google Drive API v3 (faster, no confirmation page)
- Fall back to direct download if API fails
- Better handle large files and resume support

**Note**: Without API key, the downloader still works using direct download with confirmation page handling.

You can combine both Gigafile and Google Drive auto-download:

```bash
fanbox-dl --cookie "YOUR_COOKIE" --save-html --download-gigafiles --download-drive
```

#### State Manager

Track downloaded posts to avoid re-downloading:

```bash
fanbox-dl --cookie "YOUR_COOKIE" --use-state-manager
```

This creates and uses:
- `LastSavePostId.json`: Stores the last downloaded post ID for each creator
- `.state` files: Track individual download progress for resume capability
- Automatically stops when reaching already downloaded posts
- Advances the last post ID only after a successful, unfiltered, non-dry-run crawl without `--skip-on-error`

#### Checking Download Completeness

Verify downloaded content and repair missing files without re-downloading existing files:

```bash
fanbox-dl --cookie "YOUR_COOKIE" --check
```

The check mode will:
- Scan all posts from your creators
- Verify that all expected files exist:
  - Images and files from posts
  - Text content (if `--skip-texts` was not used)
  - JSON files (if `--save-json` was enabled)
  - HTML files (if `--save-html` was enabled)
- Report any missing files with detailed information
- Display a summary of complete vs incomplete posts
- Repair missing content with the configured worker limit and wait for active downloads before exiting

**Example output:**
```
INFO Checking creator creator_id=someuser
INFO Incomplete post post_id=12345 title="Post Title"
INFO   Missing image file=0-image.jpg
INFO   Missing: JSON
WARN Check summary total=50 complete=48 incomplete=2
```

**Check specific creators:**
```bash
fanbox-dl --cookie "YOUR_COOKIE" --check --creator user1,user2
```

**Check with same options used during download:**
```bash
# If you downloaded with these options:
fanbox-dl --cookie "YOUR_COOKIE" --save-json --save-html --dir-by-post

# Check with the same options:
fanbox-dl --cookie "YOUR_COOKIE" --check --save-json --save-html --dir-by-post
```

**Note**: The `--check` flag respects all path-related options (`--save-dir`, `--dir-by-post`, `--dir-by-plan`) and content options (`--skip-files`, `--skip-images`, `--skip-texts`, `--save-json`, `--save-html`) to ensure it checks the correct locations and expected files.

**State File Management**

The state manager creates two types of files:

1. **LastSavePostId.json** - Tracks the last successfully downloaded post for each creator:
```json
{
  "creator1": 12345678,
  "creator2": 87654321
}
```

2. **Download State Files** (`.state`) - Track individual file downloads for resume:
```json
{
  "version": 1,
  "status": "downloading",
  "creatorId": "creator1",
  "postId": "12345678",
  "postTitle": "Post Title",
  "assetId": "asset123",
  "assetType": "image",
  "url": "https://...",
  "filePath": "/path/to/file.jpg",
  "bytes": 1048576,
  "totalBytes": 2097152,
  "updatedAt": "2024-01-01T12:00:00Z"
}
```

**Resume Interrupted Downloads**

If a download is interrupted, the state manager will automatically resume from where it left off:
- Partial files are saved with `.part` extension
- State files track download progress
- On restart, downloads resume using HTTP Range requests

**Cleaning Up State Files**

State files are automatically cleaned up when downloads complete successfully. If you need to manually clean up orphaned state files:

```bash
# Find and remove completed state files
find ./images -name "*.state" -type f -delete

# Remove partial download files
find ./images -name "*.part" -type f -delete
```

#### Rate Limiting

FANBOX requests are paced at 0.5 requests per second by default. This includes retries and media request headers, but not external Gigafile or Google Drive traffic. To set an explicit rate:

```bash
fanbox-dl --cookie "YOUR_COOKIE" --rate-limit 0.5
```

This allows one FANBOX request every two seconds. `--rate-limit 0` explicitly disables pacing and increases the risk of rate limits or account/session rejection.

#### Saving Original API JSON

Save the original JSON responses from the FANBOX API for each post. This can be useful for debugging or archiving purposes:

```bash
fanbox-dl --cookie "YOUR_COOKIE" --save-json
```

When enabled, each post will have a `post.json` file saved alongside downloaded media files.

#### Combining All Features

You can combine multiple features:

```bash
fanbox-dl \
  --sessid "YOUR_SESSID" \
  --save-dir "./downloads" \
  --dir-by-post \
  --save-html \
  --download-gigafiles \
  --download-drive \
  --use-state-manager \
  --rate-limit 0.5 \
  --save-json \
  --verbose \
  #--check
```

This provides a complete experience:
- Download posts with HTML generation and working hyperlinks
- Auto-download gigafile links
- Auto-download Google Drive links
- Track progress with state manager
- Avoid duplicate downloads

#### Checking Downloads After Completion

After downloading, verify that all content is complete:

```bash
# Check all downloaded content
fanbox-dl --cookie "YOUR_COOKIE" --check

# Check specific directory with same options
fanbox-dl --cookie "YOUR_COOKIE" --check --save-dir "./downloads" --dir-by-post --save-html --save-json
```

This is useful for:
- Verifying after interrupted downloads
- Checking if any files are missing
- Auditing download completeness


### Changes in v0.30.1

- Requires Go 1.27.4 and updates Brotli, compression, networking, OpenTelemetry and gRPC dependencies to address the vulnerabilities reported by CI.
- Requires tests, lint and vulnerability scanning to pass before publishing release binaries.
- Matches GOAWAY errors from the actual TLS transport so interrupted downloads can resume.
- Retains the crawling/session fixes below; use this version instead of the superseded v0.30.0 prerelease.

### Changes in v0.30.0

- Integrates upstream through `hareku/fanbox-dl@7d40c1f`, retaining this fork's export, filtering, resumable-download and external-host features.
- Supports both legacy and wrapped FANBOX API responses; skips restricted or empty post bodies safely.
- Aligns TLS/User-Agent with Chrome 146 and adds scoped, rotating cookies and session-stop protection.
- Fixes premature checkpoint/date pagination exits, old pinned-post handling, and duplicate first-page fetching. Dry runs, filtered runs and failures do not advance checkpoints.
- Fixes recursive-lock deadlocks when saving post checkpoints and supporting-creator lists.
- Bounds `--check` repairs and waits for them before exit; authentication failures no longer trigger chunk/HEAD fallback retries.
- Publishes stable version-tagged releases with checksums instead of overwriting a `dev` snapshot.

### Building from Source

Go 1.27.4 or newer is required.

```bash
# Build for current platform
go build -o fanbox-dl ./cmd/fanbox-dl

# Build for specific platform
GOOS=windows GOARCH=amd64 go build -o fanbox-dl.exe ./cmd/fanbox-dl
GOOS=darwin GOARCH=arm64 go build -o fanbox-dl-mac ./cmd/fanbox-dl
GOOS=linux GOARCH=amd64 go build -o fanbox-dl-linux ./cmd/fanbox-dl
```

