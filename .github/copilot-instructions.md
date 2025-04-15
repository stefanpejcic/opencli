{
    "python-envs.defaultEnvManager": "ms-python.python:system",
    "python-envs.pythonProjects": [],
    "codellm.provider": "ollama",
    "codellm.openai.model": "gpt-4",
    // Productivity & Workflow
    "breadcrumbs.enabled": true,
    "editor.suggestSelection": "first",
    "editor.wordWrap": "on",
    "editor.linkedEditing": true,
    "editor.bracketPairColorization.enabled": true,
    "editor.guides.bracketPairs": true,
    "editor.minimap.enabled": false,
    "editor.codeActionsOnSave": {
        "source.organizeImports": "explicit",
        "source.fixAll": "explicit"
    },
    // Advanced editor customization
    "editor.fontSize": 14,
    "editor.fontFamily": "JetBrains Mono, Consolas, 'Courier New', monospace",
    "editor.fontLigatures": true,
    "editor.cursorSmoothCaretAnimation": "on",
    "editor.stickyScroll.enabled": true,
    "editor.inlineSuggest.enabled": true,
    // File handling
    "files.autoSave": "afterDelay",
    "files.autoSaveDelay": 1000,
    "files.trimTrailingWhitespace": true,
    "files.insertFinalNewline": true,
    "files.trimFinalNewlines": true,
    // Terminal customization
    "terminal.integrated.defaultProfile.linux": "bash",
    "terminal.integrated.fontFamily": "JetBrains Mono, 'MesloLGS NF', monospace",
    "terminal.integrated.fontSize": 14,
    "terminal.integrated.lineHeight": 1.2,
    "terminal.integrated.cursorBlinking": true,
    "terminal.integrated.cursorStyle": "line",
    "terminal.integrated.copyOnSelection": true,
    "terminal.integrated.scrollback": 10000,
    "terminal.integrated.gpuAcceleration": "on",
    "terminal.integrated.tabs.enabled": true,
    "terminal.integrated.tabs.location": "right",
    "terminal.integrated.tabs.showActiveTerminal": "always",
    "terminal.integrated.persistentSessionReviveProcess": "onExitAndWindowClose",
    "terminal.integrated.shellIntegration.enabled": true,
    "terminal.integrated.shellIntegration.decorationsEnabled": "both",
    "terminal.integrated.shellIntegration.decorationIcon": "line",
    "terminal.integrated.shellIntegration.decorationIconSuccess": "check",
    "terminal.integrated.shellIntegration.decorationIconError": "x",
    "terminal.integrated.env.linux": {
        "TERM": "xterm-256color"
    },
    "terminal.integrated.defaultLocation": "view",
    "terminal.integrated.smoothScrolling": true,
    "terminal.integrated.rightClickBehavior": "selectWord",
    "terminal.integrated.splitCwd": "inherited",
    "terminal.integrated.allowChords": true,
    // Search configuration
    "search.exclude": {
        "**/node_modules": true,
        "**/venv": true,
        "**/.git": true
    },
    // Language specific
    "[python]": {
        "editor.formatOnType": true,
        "editor.defaultFormatter": "ms-python.black-formatter"
    },
    "[javascript]": {
        "editor.defaultFormatter": "esbenp.prettier-vscode"
    },
    // Window appearance
    "window.zoomLevel": 0,
    "window.menuBarVisibility": "toggle",
    "window.titleBarStyle": "custom",
    // Extensions
    "extensions.ignoreRecommendations": false,
    // Emmet
    "emmet.includeLanguages": {
        "javascript": "javascriptreact",
        "django-html": "html"
    },
    // Telemetry
    "telemetry.telemetryLevel": "off",
    // Focus settings
    "zenMode.centerLayout": false,
    "zenMode.fullScreen": false,
    "zenMode.hideLineNumbers": false,
    "editor.focusMode": "outlineView",
    // Git settings
    "git.autofetch": true,
    "git.enableSmartCommit": true,
    "git.confirmSync": false,
    "git.openRepositoryInParentFolders": "always",
    "diffEditor.ignoreTrimWhitespace": false,
    "accessibility.signals.terminalBell": {
        "sound": "off"
    },
    "continue.enableQuickActions": true,
    "cspell-sync.customToGlobalSync": true,
    "cspell-sync.newDictionarySync": {
        "enabled": false,
        "name": "project-dictionary",
        "format": "json"
    },
    "github.copilot.nextEditSuggestions.enabled": true,
    "github.copilot.chat.codesearch.enabled": true,
    "github.copilot.chat.edits.temporalContext.enabled": true,
    "github.copilot.chat.editor.temporalContext.enabled": true,
    "github.copilot.chat.languageContext.inline.typescript.enabled": true,
    "github.copilot.chat.languageContext.typescript.enabled": true,
    "github.copilot.chat.languageContext.fix.typescript.enabled": true
}
