# Notes for agents and contributors

## Browser: Brave only

The owner runs this extension in **Brave**, never Google Chrome. Chrome APIs
and "Chrome" wording in the code and docs are about the Chromium extension
platform; the browser actually in use is Brave.

- Extension pages: `brave://extensions`, not `chrome://extensions`.
- Profile data (extension storage, logs):
  `~/Library/Application Support/BraveSoftware/Brave-Browser/`, not
  `~/Library/Application Support/Google/Chrome/`.
- Native host manifest that Brave reads:
  `~/Library/Application Support/BraveSoftware/Brave-Browser/NativeMessagingHosts/com.neelbangera.lecturetranscripts.json`.
  The install/uninstall scripts and `lecture-uploader` currently write only
  the Chrome path, so after a reinstall check that the Brave copy matches.
- When debugging, reproducing, or writing setup steps, target Brave first.
