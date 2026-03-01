let audios = [];
let config = {
    closeAction: "minimize",
    dontAskAgain: false
};
let deleteTargetId = null;

function switchTab(tabId) {
    document.querySelectorAll('.tab-btn').forEach(btn => btn.classList.remove('active'));
    document.querySelectorAll('.tab-pane').forEach(pane => pane.classList.remove('active'));
    
    const buttons = document.querySelectorAll('.tab-btn');
    if (tabId === 'import') buttons[0].classList.add('active');
    if (tabId === 'settings') buttons[1].classList.add('active');
    if (tabId === 'about') buttons[2].classList.add('active');

    document.getElementById(tabId).classList.add('active');
    
    if (tabId === 'settings' || tabId === 'import') {
        loadAudios();
    }
}

function toggleSidebar() {
    const sidebar = document.querySelector('.sidebar');
    sidebar.classList.toggle('collapsed');
    
    // Save state
    const collapsed = sidebar.classList.contains('collapsed');
    if (window.go && window.go.main && window.go.main.App && window.go.main.App.SaveSidebarState) {
        window.go.main.App.SaveSidebarState(collapsed);
    }
}

function formatName(name) {
    // Remove extension
    const parts = name.split('.');
    if (parts.length > 1) parts.pop();
    let base = parts.join('.');
    
    // Truncate to 10 chars
    if (base.length > 10) {
        return base.substring(0, 10) + '...';
    }
    return base;
}

async function loadAudios() {
    if (!window.go || !window.go.main || !window.go.main.App) {
        console.warn("Wails runtime not ready");
        return;
    }
    try {
        const conf = await window.go.main.App.GetConfig();
        if (conf) {
            config.closeAction = conf.close_action || "minimize";
            config.dontAskAgain = conf.dont_ask_again || false;
            
            // Restore sidebar state
            if (conf.sidebar_collapsed) {
                document.querySelector('.sidebar').classList.add('collapsed');
            } else {
                document.querySelector('.sidebar').classList.remove('collapsed');
            }

            // Set volume
            const vol = conf.volume !== undefined ? conf.volume : 100;
            document.getElementById('volume-slider').value = vol;
            document.getElementById('volume-val').innerText = vol + '%';

            audios = conf.audio_list || [];
            
            // Load devices
            await loadDevices(conf.main_device, conf.aux_device);
        } else {
            audios = await window.go.main.App.GetAudios();
            await loadDevices();
        }
        renderImportList();
    } catch (err) {
        console.error(err);
    }
}

async function loadDevices(currentMain, currentAux) {
    try {
        const devs = await window.go.main.App.GetAudioDevices();
        const mainSelect = document.getElementById('main-device-select');
        const auxSelect = document.getElementById('aux-device-select');
        
        mainSelect.innerHTML = '';
        auxSelect.innerHTML = '<option value="none">无</option>';
        
        // Add default option
        const defOpt = document.createElement('option');
        defOpt.value = "default";
        defOpt.text = "Default Device";
        mainSelect.appendChild(defOpt);

        if (devs && devs.length > 0) {
            devs.forEach(d => {
                const opt1 = document.createElement('option');
                opt1.value = d.id;
                opt1.text = d.name;
                mainSelect.appendChild(opt1);
                
                const opt2 = document.createElement('option');
                opt2.value = d.id;
                opt2.text = d.name;
                auxSelect.appendChild(opt2);
            });
        }
        
        if (currentMain) mainSelect.value = currentMain;
        if (currentAux) auxSelect.value = currentAux;
        
    } catch (e) {
        console.error("Failed to load devices", e);
    }
}

async function saveAudioSettings() {
    const mainDev = document.getElementById('main-device-select').value;
    const auxDev = document.getElementById('aux-device-select').value;
    const vol = document.getElementById('volume-slider').value;
    
    document.getElementById('volume-val').innerText = vol + '%';
    
    await window.go.main.App.SetAudioSettings(mainDev, auxDev, parseFloat(vol));
}

async function resetAudio() {
    // console.log("Resetting audio...");
    try {
        if (!window.go || !window.go.main || !window.go.main.App || !window.go.main.App.ResetAudio) {
             throw new Error("Backend function not found. Please rebuild the app.");
        }
        await window.go.main.App.ResetAudio();
        showNotification("音频服务已重置 (配置已恢复默认)", 'success');
        
        // Reload audios/config to reflect default device settings
        await loadAudios();
    } catch (e) {
        console.error(e);
        showNotification("重置失败: " + e, 'error');
    }
}

// Ensure global scope
window.resetAudio = resetAudio;

async function importAudio() {
    try {
        const result = await window.go.main.App.ImportAudioFile();
        // Always reload audios because ImportAudioFile might have added some valid files
        // even if it returned errors for others.
        if (result && result.startsWith("Error")) {
            showNotification(result, 'error');
        } else if (result) {
            showNotification("添加成功", 'success');
        }
        // Always reload regardless of result (unless user cancelled dialog which returns empty string)
        if (result !== "") {
            loadAudios();
        }
    } catch (err) {
        console.error(err);
        showNotification("添加失败: " + err, 'error');
    }
}

async function deleteAudio(id) {
    try {
        await window.go.main.App.DeleteAudio(id);
        showNotification("删除成功", 'success');
        loadAudios();
    } catch (e) {
        showNotification("删除失败: " + e, 'error');
    }
}

function playAudio(id) {
    window.go.main.App.PlayAudioID(id);
    showNotification("正在播放", 'info');
}

let draggedItem = null;

function renderImportList() {
    const tbody = document.getElementById('import-list');
    tbody.innerHTML = '';
    if (audios.length === 0) {
        tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;color:#999;">拖入音频文件或点击右上角+添加音频</td></tr>';
        return;
    }
    audios.forEach((item, index) => {
        const tr = document.createElement('tr');
        tr.setAttribute('data-id', item.id);
        tr.className = 'audio-row'; 
        
        // Add drag handle class to first cell
        // We'll use mousedown on the row, but exclude inputs/buttons
        tr.addEventListener('mousedown', handleRowMouseDown);

        tr.innerHTML = `
            <td class="drag-handle" style="cursor: grab;">${formatName(item.name)}</td>
            <td>${item.duration}</td>
            <td>
                <input type="text" class="hotkey-input" 
                       value="${item.hotkey || ''}" 
                       readonly 
                       placeholder="点击设置"
                       onfocus="startRecording(this, '${item.id}')"
                       onblur="stopRecording(this)"
                />
            </td>
            <td>
                <button class="btn-preview" onclick="playAudio('${item.id}')">试听</button>
                <button class="btn-danger" onclick="deleteAudio('${item.id}')">删除</button>
            </td>
        `;
        tbody.appendChild(tr);
    });
}

// Global variables for sorting
let dragSrcEl = null;
let dragPlaceholder = null;
let dragGhost = null;
let startY = 0;
let initialIndex = -1;

function handleRowMouseDown(e) {
    // Ignore if clicking on input or button
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'BUTTON') {
        return;
    }
    // Only allow left click
    if (e.button !== 0) return;

    const row = e.currentTarget;
    if (!row.classList.contains('audio-row')) return;

    e.preventDefault(); // Prevent text selection

    dragSrcEl = row;
    startY = e.clientY;
    
    // Find index
    const rows = Array.from(document.querySelectorAll('.audio-row'));
    initialIndex = rows.indexOf(row);

    // Create ghost element
    const rect = row.getBoundingClientRect();
    dragGhost = row.cloneNode(true);
    dragGhost.style.position = 'fixed';
    dragGhost.style.top = rect.top + 'px';
    dragGhost.style.left = rect.left + 'px';
    dragGhost.style.width = rect.width + 'px';
    dragGhost.style.height = rect.height + 'px';
    dragGhost.style.opacity = '0.95';
    dragGhost.style.zIndex = '1000';
    dragGhost.style.pointerEvents = 'none'; // Allow events to pass through to underlying elements
    dragGhost.style.boxShadow = '0 5px 15px rgba(0,0,0,0.2)';
    dragGhost.style.background = '#fff';
    dragGhost.style.display = 'table'; // Maintain table layout
    dragGhost.style.tableLayout = 'fixed'; // Enforce column widths
    dragGhost.classList.add('dragging-ghost');
    
    // Explicitly set width for each cell in the ghost to match original
    const originalCells = row.children;
    const ghostCells = dragGhost.children;
    for (let i = 0; i < originalCells.length; i++) {
        ghostCells[i].style.width = originalCells[i].getBoundingClientRect().width + 'px';
    }

    document.body.appendChild(dragGhost);

    // Create placeholder
    dragPlaceholder = document.createElement('tr');
    dragPlaceholder.className = 'sortable-placeholder';
    dragPlaceholder.style.height = rect.height + 'px';
    // Fill with empty cells to maintain table structure
    dragPlaceholder.innerHTML = '<td colspan="4"></td>';
    
    // Insert placeholder after current row
    row.parentNode.insertBefore(dragPlaceholder, row.nextSibling);
    
    // Hide original row
    row.style.display = 'none';

    // Add global listeners
    document.addEventListener('mousemove', handleRowMouseMove);
    document.addEventListener('mouseup', handleRowMouseUp);
}

function handleRowMouseMove(e) {
    if (!dragSrcEl) return;

    // Move ghost
    const currentY = e.clientY;
    // We only track Y movement for list sorting
    // Keep X fixed or follow mouse? Let's follow mouse Y but keep X aligned?
    // Actually just follow mouse delta is easiest
    // But we set fixed position initially.
    // Let's just update top.
    const deltaY = currentY - startY;
    const initialTop = parseFloat(dragGhost.style.top);
    // This logic is flawed because startY is static. 
    // Better: update ghost position based on mouse position
    // We want the ghost to be centered under mouse vertically?
    // Or just offset.
    // Let's use transform for performance
    // But we need absolute position for hit testing? No, hit testing uses elementFromPoint
    
    // Simple approach: Set top to mouse Y - offset
    // Let's just use the initial rect logic:
    // We need to know where the mouse was relative to the row top
    // But let's just make it follow the mouse cursor
    dragGhost.style.top = (e.clientY - dragGhost.offsetHeight / 2) + 'px';
    
    // Check for swap
    // Hide ghost temporarily to find element below
    dragGhost.style.display = 'none';
    const elemBelow = document.elementFromPoint(e.clientX, e.clientY);
    dragGhost.style.display = '';

    if (!elemBelow) return;

    const rowBelow = elemBelow.closest('.audio-row');
    
    if (rowBelow && rowBelow !== dragSrcEl && rowBelow !== dragPlaceholder) {
        // Swap placeholder
        const tbody = rowBelow.parentNode;
        const rows = Array.from(tbody.children).filter(r => r !== dragSrcEl && r !== dragGhost);
        const placeholderIndex = rows.indexOf(dragPlaceholder);
        const rowBelowIndex = rows.indexOf(rowBelow);
        
        if (placeholderIndex < rowBelowIndex) {
            tbody.insertBefore(dragPlaceholder, rowBelow.nextSibling);
        } else {
            tbody.insertBefore(dragPlaceholder, rowBelow);
        }
    }
}

function handleRowMouseUp(e) {
    if (!dragSrcEl) return;

    // Remove listeners
    document.removeEventListener('mousemove', handleRowMouseMove);
    document.removeEventListener('mouseup', handleRowMouseUp);

    // Show original row at placeholder position
    dragSrcEl.style.display = '';
    dragPlaceholder.parentNode.insertBefore(dragSrcEl, dragPlaceholder);
    
    // Cleanup
    dragPlaceholder.remove();
    dragGhost.remove();
    
    // Update data
    const rows = Array.from(document.querySelectorAll('.audio-row'));
    const newOrderIds = rows.map(row => row.getAttribute('data-id'));
    
    // Check if order changed
    const currentIds = audios.map(a => a.id);
    const hasChanged = JSON.stringify(newOrderIds) !== JSON.stringify(currentIds);
    
    if (hasChanged) {
        // Update audios array
        const newAudios = [];
        newOrderIds.forEach(id => {
            const item = audios.find(a => a.id === id);
            if (item) newAudios.push(item);
        });
        audios = newAudios;
        
        // Save to backend
        if (window.go.main.App.UpdateAudioOrder) {
            window.go.main.App.UpdateAudioOrder(newOrderIds).catch(err => {
                console.error("Failed to update order:", err);
            });
        }
    }

    // Reset
    dragSrcEl = null;
    dragPlaceholder = null;
    dragGhost = null;
}

// Remove old drag handlers
// function handleDragStart... (removed)




let currentRecordingId = null;

function startRecording(input, id) {
    currentRecordingId = id;
    input.value = "按下热键...";
    input.style.borderColor = "var(--primary-color)";
    input.style.color = "var(--primary-color)";
    
    input.onkeydown = function(e) {
        e.preventDefault();
        e.stopPropagation();

        if (e.key === "Escape" || e.key === "Esc") {
            input.value = "";
            saveHotkey(id, "");
            input.blur();
            return;
        }
        
        const keys = [];
        if (e.ctrlKey) keys.push("Ctrl");
        if (e.altKey) keys.push("Alt");
        if (e.shiftKey) keys.push("Shift");
        
        const key = e.key;
        if (key !== "Control" && key !== "Alt" && key !== "Shift" && key !== "Meta") {
            let k = key.toUpperCase();
            if (key.length > 1) k = key; 
            keys.push(k);
        }
        
        const hotkeyStr = keys.join("+");
        input.value = hotkeyStr;
        
        const lastKey = keys[keys.length-1];
        if (keys.length > 0 && !["Ctrl", "Alt", "Shift"].includes(lastKey)) {
             saveHotkey(id, hotkeyStr);
             input.blur();
        }
    };
}

function stopRecording(input) {
    input.onkeydown = null;
    input.style.borderColor = "";
    input.style.color = "";
    setTimeout(loadAudios, 100);
}

async function saveHotkey(id, hotkey) {
    // Check duplicates
    if (hotkey && hotkey !== "") {
        for (const item of audios) {
            if (item.id !== id && item.hotkey === hotkey) {
                showNotification(`热键 ${hotkey} 已被 "${item.name}" 使用`, 'error');
                loadAudios(); // reload to reset input
                return;
            }
        }
    }

    await window.go.main.App.UpdateHotkey(id, hotkey);
    showNotification("热键已保存", 'success');
}

async function changeVolume(val) {
    document.getElementById('volume-val').innerText = val + '%';
    // SetVolume doesn't exist, we rely on onchange -> saveAudioSettings
    // await window.go.main.App.SetVolume(parseFloat(val));
}

async function changeDevice() {
    // const select = document.getElementById('device-select');
    // Device change is not fully supported in backend yet, just placeholder
    // await window.go.main.App.SetAudioDevice(select.value);
}

// Disable right click
document.addEventListener('contextmenu', event => event.preventDefault());

// Intercept all links and open in system browser
document.addEventListener('click', function(e) {
    // Find the closest anchor tag
    const target = e.target.closest('a');
    if (target) {
        // Always prevent default behavior (navigation, hash change, new window on Ctrl+Click)
        e.preventDefault();

        const href = target.getAttribute('href');
        // Only open in system browser if it's not an internal link
        if (href && !href.startsWith('#') && !href.startsWith('javascript:')) {
            window.go.main.App.OpenURL(target.href);
        }
    }
});

// Intercept middle click on links
document.addEventListener('auxclick', function(e) {
    if (e.button === 1) { // Middle click
        const target = e.target.closest('a');
        if (target) {
            // Always prevent default middle click behavior (new tab/window)
            e.preventDefault();

            const href = target.getAttribute('href');
            // Only open in system browser if it's not an internal link
            if (href && !href.startsWith('#') && !href.startsWith('javascript:')) {
                window.go.main.App.OpenURL(target.href);
            }
        }
    }
});

// Disable developer tools shortcuts
document.addEventListener('keydown', function(e) {
    // F12
    if (e.key === 'F12') {
        e.preventDefault();
        return;
    }
    // Ctrl+Shift+I (Inspect)
    if (e.ctrlKey && e.shiftKey && (e.key === 'I' || e.key === 'i')) {
        e.preventDefault();
        return;
    }
    // Ctrl+Shift+J (Console)
    if (e.ctrlKey && e.shiftKey && (e.key === 'J' || e.key === 'j')) {
        e.preventDefault();
        return;
    }
    // Ctrl+Shift+C (Inspect Element)
    if (e.ctrlKey && e.shiftKey && (e.key === 'C' || e.key === 'c')) {
        e.preventDefault();
        return;
    }
    // Ctrl+U (View Source)
    if (e.ctrlKey && (e.key === 'U' || e.key === 'u')) {
        e.preventDefault();
        return;
    }
});

// Disable drag and drop globally
window.addEventListener('dragover', e => e.preventDefault());
window.addEventListener('drop', e => e.preventDefault());

// Enable drag and drop for audio files in import area using Wails DragAndDrop
function setupDragAndDrop() {
    const dropZone = document.getElementById('import'); // The entire import tab
    
    if (!dropZone) return;

    // Use Wails runtime OnFileDrop
    // However, OnFileDrop is global.
    // We need to check if the drop happened on our element.
    // Wails v2 DragAndDrop option `CSSDropProperty` can help.
    // We set `--wails-drop-target: drop` on the element we want to accept drops.
    
    // Add the CSS property to enable drop on this element
    dropZone.style.setProperty('--wails-drop-target', 'drop');

    // Register the callback
    // Note: This callback receives ALL drops that match the CSS property.
    // If we had multiple drop zones, we'd need to distinguish them, but we only have one.
    // Also, we should only process if the 'import' tab is active.
    
    window.runtime.OnFileDrop((x, y, paths) => {
        // Check if import tab is active
        if (!document.getElementById('import').classList.contains('active')) {
            return;
        }

        const validPaths = [];
        for (const path of paths) {
             const lower = path.toLowerCase();
             if (lower.endsWith('.mp3') || lower.endsWith('.wav')) {
                 validPaths.push(path);
             }
        }

        if (validPaths.length > 0) {
             window.go.main.App.ImportAudioFiles(validPaths).then(result => {
                 if (result && result.startsWith("Error")) {
                     showNotification(result, 'error');
                 } else if (result) {
                     showNotification("添加成功", 'success');
                 }
                 loadAudios(); // Always reload
             });
        }
    }, true); // useDropTarget = true
}

// Call setup
setTimeout(setupDragAndDrop, 1000);


// Notification System
function showNotification(message, type = 'info') {
    const container = document.getElementById('notification-area');
    const el = document.createElement('div');
    el.className = `notification ${type}`;
    el.innerText = message;
    
    container.appendChild(el);
    
    // Auto remove
    setTimeout(() => {
        el.style.animation = 'fadeOut 0.3s ease forwards';
        setTimeout(() => el.remove(), 300);
    }, 3000);
}

// Window Controls
function winMin() {
    // Clear hover state immediately
    const btns = document.querySelectorAll('.win-btn');
    btns.forEach(btn => btn.classList.remove('hover'));
    window.go.main.App.Minimise();
}

function winMax() {
    // Clear hover state immediately
    const btns = document.querySelectorAll('.win-btn');
    btns.forEach(btn => btn.classList.remove('hover'));
    window.go.main.App.ToggleMaximise();
}

// Hover fix for close button
function setupCloseButtonHover() {
    const btns = document.querySelectorAll('.win-btn');
    btns.forEach(btn => {
        btn.addEventListener('mouseenter', () => btn.classList.add('hover'));
        btn.addEventListener('mouseleave', () => btn.classList.remove('hover'));
    });
}
// Call setup on init
setTimeout(setupCloseButtonHover, 500);

function winClose() {
    // Clear hover state immediately
    const btns = document.querySelectorAll('.win-btn');
    btns.forEach(btn => btn.classList.remove('hover'));

    if (config.dontAskAgain) {
        // Save window size before closing
        saveWindowSize().then(() => {
            if (config.closeAction === 'minimize') {
                window.go.main.App.Hide();
            } else {
                window.go.main.App.Quit();
            }
        });
    } else {
        document.getElementById('close-modal').classList.add('active');
    }
}

function closeModal() {
    document.getElementById('close-modal').classList.remove('active');
}

async function confirmClose(action) {
    const dontAsk = document.getElementById('dont-ask-checkbox').checked;
    
    // Save settings
    config.closeAction = action;
    config.dontAskAgain = dontAsk;
    await window.go.main.App.SaveSettings(action, dontAsk);
    
    // Save window size
    await saveWindowSize();

    closeModal();
    
    // Clear hover state just in case
    const btns = document.querySelectorAll('.win-btn');
    btns.forEach(btn => btn.classList.remove('hover'));

    if (action === 'minimize') {
        window.go.main.App.Hide();
        showNotification("已最小化到托盘", 'info');
    } else {
        window.go.main.App.Quit();
    }
}

async function saveWindowSize() {
    try {
        const size = await window.runtime.WindowGetSize();
        // size object structure from wails runtime is {w: number, h: number} or {Width: number, Height: number}?
        // Checking runtime.js: WindowGetSize returns Promise<Size>. Size usually has w/h or width/height.
        // Let's assume standard wails v2 runtime returns {w: ..., h: ...} or check runtime.d.ts
        // runtime.d.ts says Promise<Size>.
        // Let's log it to be safe or just pass both.
        // Wails v2 WindowGetSize usually returns struct with Width/Height in Go, but JS runtime might map it.
        // Actually, looking at runtime.js it calls window.runtime.WindowGetSize().
        // Let's try to inspect it if possible, but safer to just use what we get.
        // However, we need to add SaveWindowSize to App.go first.
        if (size) {
             await window.go.main.App.SaveWindowSize(size.w || size.width || 900, size.h || size.height || 600);
        }
    } catch (e) {
        console.error("Failed to save window size", e);
    }
}

async function checkForUpdates() {
    if (!window.go || !window.go.main || !window.go.main.App || !window.go.main.App.CheckForUpdates) {
        showUpdateModal("错误", "请先重新编译运行应用以启用更新检查功能。", false);
        return;
    }
    
    // Show loading
    const link = document.querySelector('a[onclick="checkForUpdates()"]');
    const originalText = link ? link.innerText : "检查更新";
    if (link) {
        link.innerText = "检查中...";
        link.style.pointerEvents = "none";
        link.style.color = "#999";
    }

    try {
        const result = await window.go.main.App.CheckForUpdates();
        
        if (result.error) {
            showUpdateModal("检查更新失败", result.error, false);
        } else if (result.has_update) {
            showUpdateModal(
                "发现新版本", 
                `最新版本: ${result.latest_version}<br>是否前往下载？`, 
                true, 
                result.download_url
            );
        } else {
            showUpdateModal("无更新", `当前版本已是最新`, false);
        }
    } catch (err) {
        console.error(err);
        showUpdateModal("检查更新出错", err.toString(), false);
    } finally {
        if (link) {
            link.innerText = originalText;
            link.style.pointerEvents = "auto";
            link.style.color = "var(--primary-color)";
        }
    }
}

function showUpdateModal(title, message, isUpdate, downloadUrl) {
    const modal = document.getElementById('update-modal');
    const titleEl = document.getElementById('update-title');
    const msgEl = document.getElementById('update-message');
    const actionsEl = document.getElementById('update-actions');

    titleEl.innerText = title;
    msgEl.innerHTML = message;
    actionsEl.innerHTML = '';

    if (isUpdate) {
        const confirmBtn = document.createElement('button');
        confirmBtn.className = 'btn-modal btn-primary';
        confirmBtn.innerText = '立即更新';
        
        let updateStarted = false;

        const cancelBtn = document.createElement('button');
        cancelBtn.className = 'btn-modal btn-secondary';
        cancelBtn.innerText = '稍后';
        cancelBtn.onclick = function() {
             closeUpdateModal();
        };

        confirmBtn.onclick = function() {
            if (updateStarted) return;
            updateStarted = true;

            // Change the message text as requested
            msgEl.innerHTML = ""; // Clear previous content
            
            const infoText = document.createElement('p');
            infoText.innerText = "下载完成后会自动重启软件";
            msgEl.appendChild(infoText);

            // Disable buttons
            confirmBtn.disabled = true;
            confirmBtn.innerText = '更新中...';
            cancelBtn.style.display = 'none'; // Hide cancel button during update
            
            // Show progress bar
            const progressContainer = document.createElement('div');
            progressContainer.style.width = '100%';
            progressContainer.style.height = '10px';
            progressContainer.style.background = '#eee';
            progressContainer.style.borderRadius = '5px';
            progressContainer.style.marginTop = '15px';
            progressContainer.style.overflow = 'hidden';
            
            const progressBar = document.createElement('div');
            progressBar.style.width = '0%';
            progressBar.style.height = '100%';
            progressBar.style.background = 'var(--primary-color)';
            progressBar.style.transition = 'width 0.2s';
            progressBar.id = 'update-progress-bar';
            
            progressContainer.appendChild(progressBar);
            msgEl.appendChild(progressContainer);
            
            const statusText = document.createElement('p');
            statusText.innerText = '正在下载...';
            statusText.style.fontSize = '12px';
            statusText.style.color = '#666';
            statusText.style.marginTop = '5px';
            statusText.id = 'update-status-text';
            msgEl.appendChild(statusText);
            
            // Start update
            window.go.main.App.StartUpdate(downloadUrl);
        };
        actionsEl.appendChild(confirmBtn);
        actionsEl.appendChild(cancelBtn);

        // Listen for events
        window.runtime.EventsOn("update-progress", (percentage) => {
            const bar = document.getElementById('update-progress-bar');
            if (bar) bar.style.width = percentage + '%';
        });

        window.runtime.EventsOn("update-complete", (msg) => {
            const status = document.getElementById('update-status-text');
            if (status) {
                status.innerText = "下载完成";
                status.style.color = "var(--success-color)";
            }
        });

        window.runtime.EventsOn("update-error", (err) => {
            updateStarted = false;
            const status = document.getElementById('update-status-text');
            if (status) {
                status.innerText = "错误: " + err;
                status.style.color = "var(--danger-color)";
            }
            // Re-enable confirm button as 'Retry'
            confirmBtn.disabled = false;
            confirmBtn.innerText = '重试';
            
            // Re-show cancel/close button
            cancelBtn.style.display = 'inline-block';
            cancelBtn.innerText = '关闭';
        });

    } else {
        const closeBtn = document.createElement('button');
        closeBtn.className = 'btn-modal btn-primary';
        closeBtn.innerText = '关闭';
        closeBtn.onclick = closeUpdateModal;
        actionsEl.appendChild(closeBtn);
    }

    modal.classList.add('active');
}

function closeUpdateModal() {
    document.getElementById('update-modal').classList.remove('active');
}

function openExternalLink(url) {
    window.runtime.BrowserOpenURL(url);
}

// Usage Modal
function showUsageModal() {
    const modal = document.getElementById('usage-modal');
    if(modal) modal.classList.add('active');
}

function closeUsageModal() {
    const modal = document.getElementById('usage-modal');
    if(modal) modal.classList.remove('active');
}

// Close Usage Modal when clicking outside content
window.addEventListener('click', function(event) {
    const usageModal = document.getElementById('usage-modal');
    if (event.target === usageModal) {
        closeUsageModal();
    }
});

// Initial load
const initInterval = setInterval(() => {
    if (window.go && window.go.main && window.go.main.App) {
        clearInterval(initInterval);
        loadAudios();
    }
}, 100);
