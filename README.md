# Lecture Transcripts

A Chrome extension that saves transcripts from the University of Michigan's
Leccap recordings to a GitHub repository as Markdown, organized by course.
A local macOS uploader handles GitHub access and uploads.

It saves plain and timestamped transcripts, retries temporary upload failures,
and skips recordings you've already saved. Existing files are never overwritten.
GitHub credentials stay in the macOS Keychain.

## Requirements

- An Apple silicon Mac and Chrome.
- Access to your course recordings on Leccap.
- Node.js 22 or later, npm, and Go 1.24.x.
- Xcode Command Line Tools (`xcode-select --install`).
- A GitHub account with access to the destination repository.

## Installation

### 1. Build

```sh
git clone https://github.com/neelbangera/lecture-transcripts-extension.git
cd lecture-transcripts-extension
```

Before building, set `ExpectedOwner` and `ExpectedRepo` in
[config.go](uploader/internal/config/config.go) to your GitHub repository's owner
and name. These values must match your local configuration. Uploads go to `main`.

```sh
npm ci
npm run build
scripts/build-uploader.sh
```

Keep this folder in its current location after installing; Chrome uses its path
to identify the extension and find the uploader.

### 2. Add the extension to Chrome

1. Open `chrome://extensions` and enable **Developer mode**.
2. Click **Load unpacked** and select `dist/extension` inside the project folder.
3. Copy the extension ID shown on its card.
4. Run the command below from the project folder, replacing
   `<loaded-extension-id>` with the ID you copied:

```sh
scripts/install-native-host.sh "<loaded-extension-id>"
```

Fully quit and reopen Chrome, then pin **Lecture Transcripts** to the toolbar.

### 3. Connect GitHub

Follow the [GitHub setup instructions](docs/SETUP.md#create-the-destination-repository-and-github-app)
to create a GitHub App with **Device Flow** enabled and **Contents: Read and
write**, install it on the destination repository, and create your local
`~/Library/Application Support/LectureTranscripts/config.json`. Use your
repository's owner, name, and numeric ID throughout the guide.
The repository needs at least one commit on `main`.

Open the extension popup, click **Connect GitHub**, and follow the approval
steps. Choose **Always Allow** if macOS asks for Keychain access. The popup will
show **GitHub connected** when setup is complete.

## Usage

Add your courses and terms in **Settings**, then open a supported Leccap lecture
or discussion recording while signed in. Capture is automatic by default;
check the popup for upload progress.

In **Settings**, you can manage courses and terms, turn off notifications, or
disable **Automatic capture**. With automatic capture off, click Leccap's
**Show Transcript** button to save a recording.

Saved files are organized by course, for example `eecs484/001.md` and
`eecs484/timestamped/001.md`. Discussions go in a `discussions` folder; only the
first captured section of each discussion is saved.

Uploads resume when Chrome reconnects to the uploader. If an existing file
differs from a new capture, the popup reports a conflict for you to review.

For help, see the [full setup guide](docs/SETUP.md),
[troubleshooting](docs/TROUBLESHOOTING.md), or
[security notes](docs/SECURITY.md). Development details are in
[ARCHITECTURE.md](ARCHITECTURE.md).
