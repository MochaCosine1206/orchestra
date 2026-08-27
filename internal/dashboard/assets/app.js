/* ============================================================
   Orchestra Dashboard — app.js
   Keyboard shortcuts, command palette, toast notifications,
   SSE event-driven animations, theme persistence.
   ============================================================ */

(function () {
    'use strict';

    // ============================================================
    // 1. Theme Persistence
    // ============================================================

    function initTheme() {
        var saved = localStorage.getItem('orchestra-theme');
        if (saved) {
            document.documentElement.setAttribute('data-theme', saved);
            document.cookie = 'theme=' + encodeURIComponent(saved) + ';path=/;max-age=31536000;SameSite=Lax';
        }
    }

    function setTheme(name) {
        localStorage.setItem('orchestra-theme', name);
        document.documentElement.setAttribute('data-theme', name);
        document.cookie = 'theme=' + encodeURIComponent(name) + ';path=/;max-age=31536000;SameSite=Lax';
    }

    // Apply theme immediately on load
    initTheme();

    // Expose globally for theme swatch clicks
    window.setTheme = setTheme;

    // ============================================================
    // 2. Toast Notification System
    // ============================================================

    var toastContainer = null;
    var activeToasts = [];
    var MAX_TOASTS = 3;
    var TOAST_DURATION = 4000;

    function ensureToastContainer() {
        if (toastContainer) return toastContainer;
        toastContainer = document.createElement('div');
        toastContainer.className = 'toast-container';
        // Spec: bottom-right corner
        toastContainer.style.position = 'fixed';
        toastContainer.style.bottom = 'var(--space-xl, 24px)';
        toastContainer.style.right = 'var(--space-xl, 24px)';
        toastContainer.style.top = 'auto';
        document.body.appendChild(toastContainer);
        return toastContainer;
    }

    function showToast(message, type) {
        type = type || 'info';
        var container = ensureToastContainer();

        // Evict oldest if at max
        while (activeToasts.length >= MAX_TOASTS) {
            dismissToast(activeToasts[0]);
        }

        var el = document.createElement('div');
        el.className = 'toast toast-' + type;

        var msgSpan = document.createElement('span');
        msgSpan.className = 'toast-message';
        msgSpan.textContent = message;
        el.appendChild(msgSpan);

        container.appendChild(el);
        activeToasts.push(el);

        // Auto-dismiss after 4s
        var timer = setTimeout(function () {
            dismissToast(el);
        }, TOAST_DURATION);
        el._toastTimer = timer;
    }

    function dismissToast(el) {
        if (!el || !el.parentNode) return;
        clearTimeout(el._toastTimer);
        el.classList.add('toast-fade-out');
        el.addEventListener('animationend', function () {
            if (el.parentNode) el.parentNode.removeChild(el);
        }, { once: true });
        var idx = activeToasts.indexOf(el);
        if (idx !== -1) activeToasts.splice(idx, 1);
    }

    window.showToast = showToast;

    // ============================================================
    // 3. Command Palette
    // ============================================================

    var paletteOverlay = null;
    var paletteInput = null;
    var paletteResults = null;
    var paletteItems = [];
    var paletteSelectedIdx = -1;
    var paletteSearchTimer = null;

    function createPalette() {
        if (paletteOverlay) return;

        paletteOverlay = document.createElement('div');
        paletteOverlay.className = 'command-palette-overlay';
        paletteOverlay.addEventListener('click', function (e) {
            if (e.target === paletteOverlay) closePalette();
        });

        var panel = document.createElement('div');
        panel.className = 'command-palette';

        var inputWrap = document.createElement('div');
        inputWrap.className = 'command-palette-input-wrap';

        var icon = document.createElement('span');
        icon.className = 'command-palette-icon';
        icon.textContent = '>';

        paletteInput = document.createElement('input');
        paletteInput.className = 'command-palette-input';
        paletteInput.type = 'text';
        paletteInput.placeholder = 'Search tasks, agents, sessions...';
        paletteInput.setAttribute('autocomplete', 'off');
        paletteInput.addEventListener('input', function () {
            clearTimeout(paletteSearchTimer);
            paletteSearchTimer = setTimeout(function () {
                searchPalette(paletteInput.value);
            }, 150);
        });
        paletteInput.addEventListener('keydown', handlePaletteKeydown);

        inputWrap.appendChild(icon);
        inputWrap.appendChild(paletteInput);

        paletteResults = document.createElement('div');
        paletteResults.className = 'command-palette-results';

        panel.appendChild(inputWrap);
        panel.appendChild(paletteResults);
        paletteOverlay.appendChild(panel);
        document.body.appendChild(paletteOverlay);
    }

    function openPalette() {
        createPalette();
        paletteOverlay.classList.add('open');
        paletteInput.value = '';
        paletteResults.innerHTML = '<div class="command-palette-empty">Type to search...</div>';
        paletteItems = [];
        paletteSelectedIdx = -1;
        setTimeout(function () { paletteInput.focus(); }, 50);
    }

    function closePalette() {
        if (paletteOverlay) paletteOverlay.classList.remove('open');
    }

    function isPaletteOpen() {
        return paletteOverlay && paletteOverlay.classList.contains('open');
    }

    // Fuzzy match: simple substring + character skip scoring
    function fuzzyScore(query, text) {
        if (!query) return 0;
        var q = query.toLowerCase();
        var t = text.toLowerCase();

        // Exact substring match scores highest
        if (t.indexOf(q) !== -1) return 100 + q.length;

        // Character skip matching
        var qi = 0;
        var score = 0;
        var consecutive = 0;
        for (var ti = 0; ti < t.length && qi < q.length; ti++) {
            if (t[ti] === q[qi]) {
                qi++;
                consecutive++;
                score += consecutive * 2;
            } else {
                consecutive = 0;
            }
        }
        return qi === q.length ? score : 0;
    }

    function searchPalette(query) {
        if (!query || query.trim().length === 0) {
            paletteResults.innerHTML = '<div class="command-palette-empty">Type to search...</div>';
            paletteItems = [];
            paletteSelectedIdx = -1;
            return;
        }

        fetch('/api/search?q=' + encodeURIComponent(query.trim()))
            .then(function (r) { return r.json(); })
            .then(function (results) {
                renderPaletteResults(results, query.trim());
            })
            .catch(function () {
                paletteResults.innerHTML = '<div class="command-palette-empty">Search unavailable</div>';
                paletteItems = [];
                paletteSelectedIdx = -1;
            });
    }

    function renderPaletteResults(results, query) {
        // Client-side fuzzy re-rank
        var scored = results.map(function (r) {
            return { item: r, score: fuzzyScore(query, r.title + ' ' + (r.subtitle || '')) };
        }).filter(function (s) { return s.score > 0; });
        scored.sort(function (a, b) { return b.score - a.score; });

        if (scored.length === 0) {
            paletteResults.innerHTML = '<div class="command-palette-empty">No results for "' +
                query.replace(/[<>&"]/g, '') + '"</div>';
            paletteItems = [];
            paletteSelectedIdx = -1;
            return;
        }

        // Group by type
        var groups = {};
        var groupOrder = [];
        scored.forEach(function (s) {
            var type = s.item.type || 'other';
            if (!groups[type]) {
                groups[type] = [];
                groupOrder.push(type);
            }
            groups[type].push(s.item);
        });

        var html = '';
        paletteItems = [];
        groupOrder.forEach(function (type) {
            html += '<div class="command-palette-group-label">' + type + '</div>';
            groups[type].forEach(function (item) {
                var idx = paletteItems.length;
                html += '<div class="command-palette-item" data-idx="' + idx + '" data-url="' +
                    (item.url || '').replace(/"/g, '&quot;') + '">' +
                    '<div><div class="command-palette-item-title">' +
                    (item.title || '').replace(/[<>&]/g, '') + '</div>' +
                    (item.subtitle ? '<div class="command-palette-item-sub">' +
                        item.subtitle.replace(/[<>&]/g, '') + '</div>' : '') +
                    '</div></div>';
                paletteItems.push(item);
            });
        });

        paletteResults.innerHTML = html;
        paletteSelectedIdx = 0;
        updatePaletteSelection();

        // Click handlers
        var itemEls = paletteResults.querySelectorAll('.command-palette-item');
        itemEls.forEach(function (el) {
            el.addEventListener('click', function () {
                var idx = parseInt(el.getAttribute('data-idx'), 10);
                activatePaletteItem(idx);
            });
        });
    }

    function updatePaletteSelection() {
        var items = paletteResults.querySelectorAll('.command-palette-item');
        items.forEach(function (el, i) {
            el.classList.toggle('selected', i === paletteSelectedIdx);
        });
        if (items[paletteSelectedIdx]) {
            items[paletteSelectedIdx].scrollIntoView({ block: 'nearest' });
        }
    }

    function activatePaletteItem(idx) {
        if (idx < 0 || idx >= paletteItems.length) return;
        var item = paletteItems[idx];
        closePalette();
        if (item.url) {
            window.location.href = item.url;
        }
    }

    function handlePaletteKeydown(e) {
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            if (paletteItems.length > 0) {
                paletteSelectedIdx = (paletteSelectedIdx + 1) % paletteItems.length;
                updatePaletteSelection();
            }
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            if (paletteItems.length > 0) {
                paletteSelectedIdx = (paletteSelectedIdx - 1 + paletteItems.length) % paletteItems.length;
                updatePaletteSelection();
            }
        } else if (e.key === 'Enter') {
            e.preventDefault();
            activatePaletteItem(paletteSelectedIdx);
        } else if (e.key === 'Escape') {
            e.preventDefault();
            closePalette();
        }
    }

    // ============================================================
    // 4. Shortcuts Help Overlay
    // ============================================================

    var shortcutsOverlay = null;

    var SHORTCUTS = [
        { key: 'k', desc: 'Navigate up in list' },
        { key: 'j', desc: 'Navigate down in list' },
        { key: 'Enter', desc: 'Drill into selected item' },
        { key: 'Escape', desc: 'Go back / close modal' },
        { key: '/', desc: 'Open command palette' },
        { key: 'p', desc: 'Pause / resume session' },
        { key: 'r', desc: 'Retry selected failed task' },
        { key: 's', desc: 'Stop session (with confirmation)' },
        { key: 'b', desc: 'Open briefing view' },
        { key: '?', desc: 'Show this help' }
    ];

    function createShortcutsOverlay() {
        if (shortcutsOverlay) return;

        shortcutsOverlay = document.createElement('div');
        shortcutsOverlay.className = 'shortcuts-overlay';
        shortcutsOverlay.addEventListener('click', function (e) {
            if (e.target === shortcutsOverlay) closeShortcuts();
        });

        var panel = document.createElement('div');
        panel.className = 'shortcuts-panel';

        var header = document.createElement('div');
        header.className = 'shortcuts-header';
        var title = document.createElement('span');
        title.style.fontFamily = 'var(--font-display)';
        title.style.fontSize = '16px';
        title.style.fontWeight = '700';
        title.textContent = 'Keyboard Shortcuts';
        var closeBtn = document.createElement('button');
        closeBtn.className = 'shortcuts-close';
        closeBtn.innerHTML = '&times;';
        closeBtn.addEventListener('click', closeShortcuts);
        header.appendChild(title);
        header.appendChild(closeBtn);

        var body = document.createElement('div');
        body.className = 'shortcuts-body';
        SHORTCUTS.forEach(function (s) {
            var row = document.createElement('div');
            row.className = 'shortcut-row';
            var key = document.createElement('span');
            key.className = 'shortcut-key';
            key.textContent = s.key;
            var desc = document.createElement('span');
            desc.className = 'shortcut-desc';
            desc.textContent = s.desc;
            row.appendChild(desc);
            row.appendChild(key);
            body.appendChild(row);
        });

        panel.appendChild(header);
        panel.appendChild(body);
        shortcutsOverlay.appendChild(panel);
        document.body.appendChild(shortcutsOverlay);
    }

    function openShortcuts() {
        createShortcutsOverlay();
        shortcutsOverlay.classList.add('open');
    }

    function closeShortcuts() {
        if (shortcutsOverlay) shortcutsOverlay.classList.remove('open');
    }

    function isShortcutsOpen() {
        return shortcutsOverlay && shortcutsOverlay.classList.contains('open');
    }

    // ============================================================
    // 5. List Navigation Helpers
    // ============================================================

    function getNavigableItems() {
        // Task rows, project rows, or any .trow elements
        return document.querySelectorAll('.trow[data-task-id], .task-row, .project-row');
    }

    function getSelectedIndex(items) {
        for (var i = 0; i < items.length; i++) {
            if (items[i].classList.contains('selected')) return i;
        }
        return -1;
    }

    function selectItem(items, idx) {
        items.forEach(function (el) { el.classList.remove('selected'); });
        if (idx >= 0 && idx < items.length) {
            items[idx].classList.add('selected');
            items[idx].scrollIntoView({ block: 'nearest', behavior: 'smooth' });
        }
    }

    function drillIntoSelected() {
        var items = getNavigableItems();
        var idx = getSelectedIndex(items);
        if (idx === -1) return;
        var el = items[idx];
        // Trigger HTMX click or normal navigation
        if (el.hasAttribute('hx-get') || el.hasAttribute('hx-post')) {
            el.click();
        } else if (el.querySelector('a')) {
            el.querySelector('a').click();
        } else {
            el.click();
        }
    }

    // ============================================================
    // 6. Keyboard Shortcuts — Main Keydown Handler
    // ============================================================

    function isInputFocused() {
        var tag = document.activeElement && document.activeElement.tagName;
        return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' ||
            (document.activeElement && document.activeElement.isContentEditable);
    }

    document.addEventListener('keydown', function (e) {
        // Suppress shortcuts when focused on input/textarea
        if (isInputFocused()) return;

        // Escape: close modal/palette/shortcuts/detail panel, or go back
        if (e.key === 'Escape') {
            if (isPaletteOpen()) { closePalette(); e.preventDefault(); return; }
            if (isShortcutsOpen()) { closeShortcuts(); e.preventDefault(); return; }
            // Close detail panel if open
            if (typeof window.closeDetailPanel === 'function') {
                var panel = document.querySelector('.detail-panel.open, .detail-overlay.open');
                if (panel) { window.closeDetailPanel(); e.preventDefault(); return; }
            }
            // Close any modal overlay
            var modal = document.querySelector('.modal-overlay.open');
            if (modal) { modal.classList.remove('open'); e.preventDefault(); return; }
            return;
        }

        // Don't handle shortcuts when palette or shortcuts overlay is open
        if (isPaletteOpen() || isShortcutsOpen()) return;

        var key = e.key;

        switch (key) {
            case '/':
                e.preventDefault();
                openPalette();
                break;

            case '?':
                e.preventDefault();
                openShortcuts();
                break;

            case 'k': {
                e.preventDefault();
                var items = getNavigableItems();
                if (items.length === 0) break;
                var cur = getSelectedIndex(items);
                var next = cur <= 0 ? items.length - 1 : cur - 1;
                selectItem(items, next);
                break;
            }

            case 'j': {
                e.preventDefault();
                var items2 = getNavigableItems();
                if (items2.length === 0) break;
                var cur2 = getSelectedIndex(items2);
                var next2 = cur2 >= items2.length - 1 ? 0 : cur2 + 1;
                selectItem(items2, next2);
                break;
            }

            case 'Enter':
                e.preventDefault();
                drillIntoSelected();
                break;

            case 'p':
                e.preventDefault();
                togglePauseResume();
                break;

            case 'r':
                e.preventDefault();
                retrySelectedTask();
                break;

            case 's':
                e.preventDefault();
                stopSession();
                break;

            case 'b':
                e.preventDefault();
                window.location.href = '/briefing';
                break;
        }
    });

    // ============================================================
    // 7. Actions: Pause/Resume, Retry, Stop
    // ============================================================

    function togglePauseResume() {
        fetch('/api/factory/steering', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ action: 'toggle-pause' })
        }).then(function (r) {
            if (r.ok) showToast('Session toggled', 'info');
            else showToast('Failed to toggle session', 'error');
        }).catch(function () {
            showToast('Network error', 'error');
        });
    }

    function retrySelectedTask() {
        var items = getNavigableItems();
        var idx = getSelectedIndex(items);
        if (idx === -1) { showToast('No task selected', 'warning'); return; }
        var el = items[idx];
        var taskId = el.getAttribute('data-task-id');
        if (!taskId) { showToast('Selected item is not a task', 'warning'); return; }

        fetch('/api/task/' + encodeURIComponent(taskId) + '/retry', {
            method: 'POST'
        }).then(function (r) {
            if (r.ok) showToast('Retrying task ' + taskId, 'success');
            else showToast('Failed to retry task', 'error');
        }).catch(function () {
            showToast('Network error', 'error');
        });
    }

    function stopSession() {
        if (!confirm('Stop the current session? This will trigger an emergency stop.')) return;
        fetch('/api/factory/emergency-stop', {
            method: 'POST'
        }).then(function (r) {
            if (r.ok) showToast('Session stopped', 'warning');
            else showToast('Failed to stop session', 'error');
        }).catch(function () {
            showToast('Network error', 'error');
        });
    }

    // ============================================================
    // 8. SSE Event Listener — Animation Dispatch
    // ============================================================

    function initSSE() {
        var evtSource;
        try {
            evtSource = new EventSource('/api/events');
        } catch (e) {
            return;
        }

        evtSource.addEventListener('task_update', function (e) {
            handleSSEEvent(e, 'task');
        });

        evtSource.addEventListener('agent_update', function (e) {
            handleSSEEvent(e, 'agent');
        });

        evtSource.addEventListener('toast', function (e) {
            try {
                var data = JSON.parse(e.data);
                showToast(data.message || e.data, data.type || 'info');
            } catch (err) {
                showToast(e.data, 'info');
            }
        });

        evtSource.addEventListener('message', function (e) {
            try {
                var data = JSON.parse(e.data);
                if (data.event_type) {
                    dispatchAnimation(data);
                }
                if (data.toast) {
                    showToast(data.toast, data.toast_type || 'info');
                }
            } catch (err) {
                // Ignore non-JSON messages
            }
        });

        evtSource.onerror = function () {
            // Reconnect handled by EventSource automatically
        };
    }

    function handleSSEEvent(e, entityType) {
        try {
            var data = JSON.parse(e.data);
            dispatchAnimation(data);
        } catch (err) {
            // Ignore parse errors
        }
    }

    // Map event types to CSS animation classes
    var ANIMATION_MAP = {
        // Task lifecycle
        'task_spawned': 'anim-spawn',
        'task_started': 'anim-running',
        'task_completed': 'anim-complete',
        'task_failed': 'anim-fail',
        'task_retrying': 'anim-retry',
        // Agent states
        'agent_connected': 'anim-connect',
        'agent_disconnected': 'anim-disconnect',
        'agent_heartbeat': 'anim-heartbeat',
        // Merge pipeline
        'merge_started': 'anim-merge',
        'merge_completed': 'anim-merge-done',
        'merge_conflict': 'anim-conflict'
    };

    function dispatchAnimation(data) {
        var eventType = data.event_type || data.type;
        var animClass = ANIMATION_MAP[eventType];
        if (!animClass) return;

        var targets = [];

        // Match by task ID
        if (data.task_id) {
            var taskEls = document.querySelectorAll('[data-task-id="' + data.task_id + '"]');
            taskEls.forEach(function (el) { targets.push(el); });
        }

        // Match by agent ID
        if (data.agent_id) {
            var agentEls = document.querySelectorAll('[data-agent-id="' + data.agent_id + '"]');
            agentEls.forEach(function (el) { targets.push(el); });
        }

        targets.forEach(function (el) {
            // Remove previous animation classes
            Object.values(ANIMATION_MAP).forEach(function (cls) {
                el.classList.remove(cls);
            });
            // Force reflow to restart animation
            void el.offsetWidth;
            el.classList.add(animClass);

            // Remove animation class after completion
            el.addEventListener('animationend', function handler() {
                el.classList.remove(animClass);
                el.removeEventListener('animationend', handler);
            });
        });
    }

    // ============================================================
    // 9. HTMX HX-Trigger Toast Integration
    // ============================================================

    document.addEventListener('htmx:afterRequest', function (e) {
        var trigger = e.detail.xhr && e.detail.xhr.getResponseHeader('HX-Trigger');
        if (!trigger) return;
        try {
            var parsed = JSON.parse(trigger);
            if (parsed.showToast) {
                var t = parsed.showToast;
                showToast(t.message || t, t.type || 'info');
            }
        } catch (err) {
            // HX-Trigger might be a simple string event name
        }
    });

    // ============================================================
    // 10. Initialize on DOMContentLoaded
    // ============================================================

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    function init() {
        initTheme();
        initSSE();
    }

})();
