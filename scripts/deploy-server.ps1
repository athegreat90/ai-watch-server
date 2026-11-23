. "$PSScriptRoot/common.ps1"
Push-Location $Root
try {
    Invoke-Checked docker @('compose','config','--quiet')
    Invoke-Checked docker @('compose','build')
    if (-not (Test-Path bridge/config.local.json)) {
        Invoke-Checked docker @('run','--rm','--entrypoint','/app/ai-watch-server','-v',"$Root/bridge:/cfg",'ai-monitor-server:local','init-config','--config','/cfg/config.local.json')
    }
    Invoke-Checked docker @('compose','up','-d','--wait','--wait-timeout','90')
    Invoke-Checked docker @('compose','ps')
    Invoke-Checked docker @('compose','exec','-T','ai-monitor-server','/app/ai-watch-check-api','--url','http://127.0.0.1:8765','--token-file','/app/config.local.json')
} finally { Pop-Location }
