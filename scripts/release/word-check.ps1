# AC-041 Word check (PRD 12; docs/release/README.md "Operator evidence"). Run by hand on a
# Windows machine with Microsoft Word, as the desktop user (Word automation does not run as
# SYSTEM):
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\release\word-check.ps1 `
#     -File docs\release\evidence\AC-041-word.docx -Sha256 <hex from acceptance-evidence.json>
#
# It opens the DOCX twice, each time in a Word instance of its own, read-only:
#   1. alerts suppressed and OpenAndRepair off: a damaged package raises an error, which fails;
#   2. alerts on: any prompt Word raises (a repair prompt included) holds the open, so an open
#      that has not returned within -PromptSeconds fails, and only the Word process this script
#      started is stopped.
# Exit 0: the file has that SHA-256, both opens returned with no error and no prompt, and the
# original text and the editor's edit are in it. Exit 1 otherwise. Paste the output and the
# exit code into the evidence record.
param([Parameter(Mandatory)][string]$File, [Parameter(Mandatory)][string]$Sha256, [int]$PromptSeconds = 60)
$ErrorActionPreference = 'Stop'
$path = (Resolve-Path $File).Path
$h = (Get-FileHash $path -Algorithm SHA256).Hash.ToLower()
"file " + (Split-Path $path -Leaf) + " sha256 " + $h + " bytes " + (Get-Item $path).Length
if ($h -ne $Sha256.ToLower()) { "FAIL sha256 is not " + $Sha256; exit 1 }

$open = {
  param($p, $alerts)
  $w = New-Object -ComObject Word.Application
  try {
    $w.Visible = $false
    $w.DisplayAlerts = $alerts
    $v = "Word " + $w.Version + " build " + $w.Build
    # Open(FileName, ConfirmConversions, ReadOnly, AddToRecentFiles, PasswordDocument, PasswordTemplate,
    #      Revert, WritePasswordDocument, WritePasswordTemplate, Format, Encoding, Visible, OpenAndRepair)
    $doc = $w.Documents.Open($p, $false, $true, $false, '', '', $false, '', '', 0, [Type]::Missing, $false, $false)
    $text = $doc.Content.Text
    $r = [pscustomobject]@{ Word = $v; Paragraphs = $doc.Paragraphs.Count; Words = $doc.ComputeStatistics(0)
      Compat = $doc.CompatibilityMode; Edit = $text.Contains('Typed in the browser editor.')
      Original = $text.Contains('Synthetic browser document.'); Error = $null }
    $doc.Close(0)
    $r
  } catch {
    [pscustomobject]@{ Word = $v; Error = $_.Exception.Message }
  } finally {
    $w.Quit()
    [void][Runtime.InteropServices.Marshal]::ReleaseComObject($w)
  }
}

$ok = $true
foreach ($pass in @(@{ Name = 'alerts off'; Alerts = 0 }, @{ Name = 'alerts on'; Alerts = -1 })) {
  $before = @(Get-Process WINWORD -ErrorAction SilentlyContinue | ForEach-Object Id)
  $job = Start-Job -ScriptBlock $open -ArgumentList $path, $pass.Alerts
  if (-not (Wait-Job $job -Timeout $PromptSeconds)) {
    "FAIL " + $pass.Name + ": the open did not return within " + $PromptSeconds + "s, so Word is holding it at a prompt"
    Get-Process WINWORD -ErrorAction SilentlyContinue | Where-Object { $before -notcontains $_.Id } | Stop-Process -Force
    Remove-Job $job -Force
    $ok = $false
    continue
  }
  $r = Receive-Job $job
  Remove-Job $job -Force
  if ($pass.Alerts -eq 0) { $r.Word }
  if ($r.Error) { "FAIL " + $pass.Name + ": " + $r.Error; $ok = $false; continue }
  $pass.Name + ": opened with no repair and no prompt: " + $r.Paragraphs + " paragraphs, " + $r.Words + " words, compat mode " + $r.Compat + ", edit text " + $r.Edit + ", original text " + $r.Original
  if (-not ($r.Edit -and $r.Original)) { "FAIL " + $pass.Name + ": the saved document is missing the original text or the edit"; $ok = $false }
}
if ($ok) { "PASS"; exit 0 } else { exit 1 }
