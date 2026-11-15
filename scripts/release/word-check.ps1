# AC-041 Word check (PRD 12; docs/release/README.md "Operator evidence"). Run by hand on a
# Windows machine with Microsoft Word, as the desktop user (Word automation does not run as
# SYSTEM). It opens the DOCX the browser editor saved read-only, with OpenAndRepair off and
# alerts suppressed, so a damaged package raises an error instead of being repaired silently.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\release\word-check.ps1 `
#     -File docs\release\evidence\AC-041-word.docx -Sha256 <hex from acceptance-evidence.json>
#
# Exit 0: the file has that SHA-256, Word opened it with no error, and both the original text
# and the editor's edit are in it. Exit 1 otherwise. Paste the output and the exit code into
# the evidence record.
param([Parameter(Mandatory)][string]$File, [Parameter(Mandatory)][string]$Sha256)
$ErrorActionPreference = 'Stop'
$path = (Resolve-Path $File).Path
$h = (Get-FileHash $path -Algorithm SHA256).Hash.ToLower()
"file " + (Split-Path $path -Leaf) + " sha256 " + $h + " bytes " + (Get-Item $path).Length
if ($h -ne $Sha256.ToLower()) { "FAIL sha256 is not " + $Sha256; exit 1 }
$w = New-Object -ComObject Word.Application
$ok = $false
try {
  $w.Visible = $false
  $w.DisplayAlerts = 0
  "Word " + $w.Version + " build " + $w.Build
  # Open(FileName, ConfirmConversions, ReadOnly, AddToRecentFiles, PasswordDocument, PasswordTemplate,
  #      Revert, WritePasswordDocument, WritePasswordTemplate, Format, Encoding, Visible, OpenAndRepair)
  $doc = $w.Documents.Open($path, $false, $true, $false, '', '', $false, '', '', 0, [Type]::Missing, $false, $false)
  $text = $doc.Content.Text
  $edit = $text.Contains('Typed in the browser editor.')
  $orig = $text.Contains('Synthetic browser document.')
  "opened with no repair: " + $doc.Paragraphs.Count + " paragraphs, " + $doc.ComputeStatistics(0) + " words, compat mode " + $doc.CompatibilityMode + ", edit text " + $edit + ", original text " + $orig
  $doc.Close(0)
  $ok = $edit -and $orig
  if (-not $ok) { "FAIL the saved document is missing the original text or the edit" }
} catch {
  "FAIL " + $_.Exception.Message
} finally {
  $w.Quit()
  [void][Runtime.InteropServices.Marshal]::ReleaseComObject($w)
}
if ($ok) { "PASS"; exit 0 } else { exit 1 }
