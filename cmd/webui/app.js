let channelsData = [];
let curConvSource = 'openai';
let curConvTarget = 'claude';
let modelStatusMap = {};

function computeFullTargetURL(provider, baseUrl) {
    let base = (baseUrl || '').trim();
    if (!base) {
        if (provider === 'claude') base = 'https://api.anthropic.com';
        else if (provider === 'deepseek') base = 'https://api.deepseek.com';
        else if (provider === 'gemini') base = 'https://generativelanguage.googleapis.com';
        else if (provider === 'ollama') base = 'http://localhost:11434';
        else base = 'https://api.openai.com';
    }
    base = base.replace(/\/+$/, '');

    if (provider === 'claude') {
        if (base.endsWith('/v1/messages')) return base;
        if (base.endsWith('/v1')) return base + '/messages';
        return base + '/v1/messages';
    } else if (provider === 'gemini') {
        return base + '/v1beta/models/{model}:generateContent';
    } else {
        if (base.endsWith('/v1/chat/completions')) return base;
        if (base.endsWith('/v1')) return base + '/chat/completions';
        return base + '/v1/chat/completions';
    }
}

function switchView(viewName, btn) {
    document.querySelectorAll('.view-section').forEach(el => el.classList.remove('active'));
    document.querySelectorAll('.nav-btn').forEach(el => el.classList.remove('active'));
    document.getElementById('view-' + viewName).classList.add('active');
    btn.classList.add('active');
    if (viewName === 'converter') loadSample('simple');
}

async function fetchConfig() {
    try {
        const resp = await fetch('/api/config');
        const data = await resp.json();
        if (data.success) {
            channelsData = data.channels || [];
            renderChannels();
        }
    } catch (e) {
        console.error('获取配置失败', e);
    }
}

function renderChannels() {
    const container = document.getElementById('channelsContainer');
    container.innerHTML = '';

    channelsData.forEach((ch, idx) => {
        const card = document.createElement('div');
        card.className = 'channel-card';
        const fullUrl = computeFullTargetURL(ch.provider, ch.base_url);
        const models = ch.models || [];
        const enabledCount = models.filter(m => m.enabled).length;

        card.innerHTML = `
            <div class="channel-header">
                <div class="channel-title">
                    🏷️ 渠道 #${idx + 1}: ${ch.provider.toUpperCase()} · <span style="font-size:0.85rem; color:var(--text-secondary); font-weight:normal;">${ch.name || '未命名渠道'}</span>
                </div>
                <div style="display:flex; gap:8px;">
                    <button class="btn-danger btn-sm" onclick="removeChannel(${idx})">删除渠道</button>
                </div>
            </div>
            
            <div style="display:grid; grid-template-columns: 1.5fr 1fr 2fr; gap:12px;">
                <div class="form-row">
                    <label>渠道备注名 (Name)</label>
                    <input type="text" placeholder="如：Claude 官方主渠道" value="${ch.name || ''}" oninput="updateChannelField(${idx}, 'name', this.value)">
                </div>
                <div class="form-row">
                    <label>厂商协议类型 (Provider)</label>
                    <select onchange="updateChannelField(${idx}, 'provider', this.value); refreshCardURL(${idx});">
                        <option value="claude" ${ch.provider==='claude'?'selected':''}>Anthropic Claude (自动转 Messages)</option>
                        <option value="deepseek" ${ch.provider==='deepseek'?'selected':''}>DeepSeek (OpenAI 兼容)</option>
                        <option value="openai" ${ch.provider==='openai'?'selected':''}>OpenAI 官方</option>
                        <option value="gemini" ${ch.provider==='gemini'?'selected':''}>Google Gemini</option>
                        <option value="ollama" ${ch.provider==='ollama'?'selected':''}>本地 Ollama</option>
                    </select>
                </div>
                <div class="form-row">
                    <label>上游 API Key</label>
                    <input type="password" placeholder="sk-ant-... / sk-..." value="${ch.api_key || ''}" oninput="updateChannelField(${idx}, 'api_key', this.value)">
                </div>
            </div>

            <div class="form-row">
                <label>上游 Base URL</label>
                <input type="text" placeholder="https://api.anthropic.com" value="${ch.base_url || ''}" oninput="updateChannelField(${idx}, 'base_url', this.value); refreshCardURL(${idx});">
                <div class="url-preview-hint" id="urlHint-${idx}">
                    <strong>🔗 实际完整请求端点:</strong> <code>${fullUrl}</code>
                </div>
            </div>

            <!-- 多模型配置表格 -->
            <div class="models-section">
                <div class="models-header">
                    <div>
                        <strong>📋 该渠道模型列表与参数</strong> 
                        <span style="color:var(--text-secondary);">（已启用 <span id="selCount-${idx}" style="color:var(--accent); font-weight:bold;">${enabledCount}</span> / ${models.length} 个模型）</span>
                    </div>
                    <div style="display:flex; gap:6px;">
                        <button class="btn-primary btn-sm" onclick="fetchModelsForChannel(${idx}, this)">🔄 拉取上游模型</button>
                        <button class="btn-secondary btn-sm" onclick="addNewModelRow(${idx})">➕ 添加模型</button>
                        <button class="btn-secondary btn-sm" onclick="toggleAllModelsInChannel(${idx}, true)">全选</button>
                        <button class="btn-secondary btn-sm" onclick="toggleAllModelsInChannel(${idx}, false)">清空</button>
                        <button class="btn-success btn-sm" onclick="testAllEnabledModels(${idx}, this)">⚡ 批量测速已启用模型</button>
                    </div>
                </div>

                <div style="overflow-x:auto;">
                    <table class="model-table">
                        <thead>
                            <tr>
                                <th style="width:40px; text-align:center;">启用</th>
                                <th style="width:200px;">上游真实模型 ID</th>
                                <th style="width:160px;">对外别名 (Alias)</th>
                                <th style="width:120px;">上下文容量</th>
                                <th style="width:120px;">最大输出</th>
                                <th style="width:130px;">🧠 思考强度</th>
                                <th style="width:100px;">思考预算</th>
                                <th style="width:130px;">状态 / 测速</th>
                                <th style="width:50px; text-align:center;">操作</th>
                            </tr>
                        </thead>
                        <tbody id="modelTableBody-${idx}">
                            ${renderModelTableRows(idx, ch)}
                        </tbody>
                    </table>
                </div>
            </div>
        `;
        container.appendChild(card);
    });
}

function renderModelTableRows(chIdx, ch) {
    const models = ch.models || [];
    if (models.length === 0) {
        return `<tr><td colspan="9" style="text-align:center; color:var(--text-muted); padding:16px;">暂无模型，点击【🔄 拉取上游模型】或【➕ 添加模型】。</td></tr>`;
    }

    return models.map((m, mIdx) => {
        const modelId = m.id || m.ID || '';
        const modelAlias = m.alias || m.Alias || modelId;
        const statusKey = chIdx + '_' + modelId;
        const stat = modelStatusMap[statusKey];
        let statusBadge = '<span style="color:var(--text-muted); font-size:0.75rem;">未测试</span>';
        if (stat) {
            if (stat.status === 'testing') {
                statusBadge = `<span class="model-status-badge status-testing">⏱️ 测速中</span>`;
            } else if (stat.status === 'ok') {
                statusBadge = `<span class="model-status-badge status-ok">✅ ${stat.latency}ms</span>`;
            } else {
                statusBadge = `<span class="model-status-badge status-err" title="${stat.error||''}">❌ 失败</span>`;
            }
        }

        const ctx = m.context_tokens || 128000;
        const maxOut = m.max_output_tokens || 4096;
        const thinking = m.thinking_mode || 'off';
        const budget = m.thinking_budget || 0;

        return `
            <tr>
                <td style="text-align:center;">
                    <input type="checkbox" ${m.enabled ? 'checked' : ''} onchange="updateModelField(${chIdx}, ${mIdx}, 'enabled', this.checked); updateEnabledCounter(${chIdx});">
                </td>
                <td>
                    <input type="text" class="table-input" value="${modelId}" placeholder="如 claude-3-7-sonnet-20250219" oninput="updateModelField(${chIdx}, ${mIdx}, 'id', this.value)">
                </td>
                <td>
                    <input type="text" class="table-input" value="${modelAlias}" placeholder="如 claude-3-7-sonnet" oninput="updateModelField(${chIdx}, ${mIdx}, 'alias', this.value)">
                </td>
                <td>
                    <select class="table-select" onchange="updateModelField(${chIdx}, ${mIdx}, 'context_tokens', parseInt(this.value))">
                        <option value="200000" ${ctx===200000?'selected':''}>200K (Claude / o3)</option>
                        <option value="256000" ${ctx===256000||ctx===262144?'selected':''}>256K (Qwen / DeepSeek)</option>
                        <option value="512000" ${ctx===512000||ctx===524288?'selected':''}>512K (超长上下文)</option>
                        <option value="1000000" ${ctx===1000000||ctx===1048576||ctx===2097152?'selected':''}>1M (Gemini / 百万级)</option>
                        <option value="128000" ${ctx===128000?'selected':''}>128K (GPT-4o)</option>
                    </select>
                </td>
                <td>
                    <select class="table-select" onchange="updateModelField(${chIdx}, ${mIdx}, 'max_output_tokens', parseInt(this.value))">
                        <option value="65536" ${maxOut===65536||maxOut===64000?'selected':''}>64K (65,536 - 主流标配大输出)</option>
                        <option value="131072" ${maxOut===131072||maxOut===128000?'selected':''}>128K (131,072 - 超长/深度思考链)</option>
                        <option value="32768" ${maxOut===32768?'selected':''}>32K (32,768 - 常用长生成)</option>
                        <option value="16384" ${maxOut===16384?'selected':''}>16K (16,384 - 标准输出)</option>
                        <option value="8192" ${maxOut===8192?'selected':''}>8K (8,192 - 基础轻量)</option>
                    </select>
                </td>
                <td>
                    <select class="table-select" onchange="onThinkingModeChange(${chIdx}, ${mIdx}, this.value)">
                        <option value="off" ${thinking==='off'?'selected':''}>🚫 关闭思考 (Off)</option>
                        <option value="auto" ${thinking==='auto'?'selected':''}>⚡ 动态自适应 (Auto)</option>
                        <option value="minimal" ${thinking==='minimal'?'selected':''}>🟢 轻度快速 (~2K)</option>
                        <option value="low" ${thinking==='low'?'selected':''}>🟢 低强度 (~4K)</option>
                        <option value="medium" ${thinking==='medium'?'selected':''}>🟡 中强度 (~8K)</option>
                        <option value="high" ${thinking==='high'?'selected':''}>🟣 高强度 (~16K-32K)</option>
                        <option value="max" ${thinking==='max'||thinking==='ultra'?'selected':''}>🔴 满血极限 (~64K)</option>
                        <option value="budget" ${thinking==='budget'?'selected':''}>⚙️ 自定义 Tokens</option>
                    </select>
                </td>
                <td>
                    <input type="number" id="budgetInput-${chIdx}-${mIdx}" class="table-input" value="${budget}" placeholder="0=默认" oninput="updateModelField(${chIdx}, ${mIdx}, 'thinking_budget', parseInt(this.value)||0)">
                </td>
                <td>
                    <div style="display:flex; align-items:center; gap:6px;">
                        ${statusBadge}
                        <button class="btn-secondary btn-sm" style="padding:1px 6px; font-size:0.7rem;" onclick="testSingleModel(${chIdx}, ${mIdx}, this)">测速</button>
                    </div>
                </td>
                <td style="text-align:center;">
                    <button class="btn-danger btn-sm" style="padding:1px 6px; font-size:0.7rem;" onclick="removeModelRow(${chIdx}, ${mIdx})">✕</button>
                </td>
            </tr>
        `;
    }).join('');
}

function refreshCardURL(idx) {
    const ch = channelsData[idx];
    const hintEl = document.getElementById('urlHint-' + idx);
    if (hintEl) {
        const fullUrl = computeFullTargetURL(ch.provider, ch.base_url);
        hintEl.innerHTML = `<strong>🔗 实际完整请求端点:</strong> <code>${fullUrl}</code>`;
    }
}

function updateChannelField(idx, field, val) {
    channelsData[idx][field] = val;
}

function updateModelField(chIdx, mIdx, field, val) {
    if (!channelsData[chIdx].models) channelsData[chIdx].models = [];
    channelsData[chIdx].models[mIdx][field] = val;
}

function onThinkingModeChange(chIdx, mIdx, mode) {
    updateModelField(chIdx, mIdx, 'thinking_mode', mode);
    let budget = 0;
    if (mode === 'minimal') budget = 2048;
    else if (mode === 'low') budget = 4096;
    else if (mode === 'medium') budget = 8192;
    else if (mode === 'high') budget = 16384;
    else if (mode === 'max' || mode === 'ultra') budget = 64000;
    else if (mode === 'budget') {
        const cur = channelsData[chIdx].models[mIdx].thinking_budget;
        budget = cur > 0 ? cur : 4096;
    }
    updateModelField(chIdx, mIdx, 'thinking_budget', budget);
    const bInput = document.getElementById(`budgetInput-${chIdx}-${mIdx}`);
    if (bInput) bInput.value = budget;
}

function updateEnabledCounter(chIdx) {
    const ch = channelsData[chIdx];
    const enabledCount = (ch.models || []).filter(m => m.enabled).length;
    const counterEl = document.getElementById('selCount-' + chIdx);
    if (counterEl) counterEl.innerText = enabledCount;
}

function addNewModelRow(chIdx) {
    const ch = channelsData[chIdx];
    if (!ch.models) ch.models = [];
    ch.models.push({
        id: "custom-model",
        alias: "custom-model",
        enabled: true,
        context_tokens: 128000,
        max_output_tokens: 4096,
        thinking_mode: "off",
        thinking_budget: 0
    });
    document.getElementById('modelTableBody-' + chIdx).innerHTML = renderModelTableRows(chIdx, ch);
    updateEnabledCounter(chIdx);
}

function removeModelRow(chIdx, mIdx) {
    channelsData[chIdx].models.splice(mIdx, 1);
    document.getElementById('modelTableBody-' + chIdx).innerHTML = renderModelTableRows(chIdx, channelsData[chIdx]);
    updateEnabledCounter(chIdx);
}

function toggleAllModelsInChannel(chIdx, isEnable) {
    const ch = channelsData[chIdx];
    (ch.models || []).forEach(m => m.enabled = isEnable);
    document.getElementById('modelTableBody-' + chIdx).innerHTML = renderModelTableRows(chIdx, ch);
    updateEnabledCounter(chIdx);
}

async function fetchModelsForChannel(chIdx, btn) {
    const ch = channelsData[chIdx];
    btn.disabled = true;
    btn.innerText = '⏳ 拉取中...';

    try {
        const resp = await fetch('/api/fetch_models', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                provider: ch.provider,
                api_key: ch.api_key,
                base_url: ch.base_url
            })
        });
        const res = await resp.json();
        btn.disabled = false;
        btn.innerText = '🔄 拉取上游模型';

        if (res.success && res.models) {
            ch.models = res.models;
            document.getElementById('modelTableBody-' + chIdx).innerHTML = renderModelTableRows(chIdx, ch);
            updateEnabledCounter(chIdx);
        } else {
            alert('拉取失败: ' + (res.error || '未知错误'));
        }
    } catch (e) {
        btn.disabled = false;
        btn.innerText = '🔄 拉取上游模型';
        alert('请求异常: ' + e.message);
    }
}

async function testSingleModel(chIdx, mIdx, btn) {
    const ch = channelsData[chIdx];
    const m = ch.models[mIdx];
    const modelId = m.id || m.ID;
    const statusKey = chIdx + '_' + modelId;
    modelStatusMap[statusKey] = { status: 'testing' };
    document.getElementById('modelTableBody-' + chIdx).innerHTML = renderModelTableRows(chIdx, ch);

    try {
        const resp = await fetch('/api/test_model', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                provider: ch.provider,
                api_key: ch.api_key,
                base_url: ch.base_url,
                model: modelId
            })
        });
        const res = await resp.json();
        if (res.success) {
            modelStatusMap[statusKey] = { status: 'ok', latency: res.latency_ms };
        } else {
            modelStatusMap[statusKey] = { status: 'err', error: res.error };
        }
    } catch (e) {
        modelStatusMap[statusKey] = { status: 'err', error: e.message };
    }
    document.getElementById('modelTableBody-' + chIdx).innerHTML = renderModelTableRows(chIdx, ch);
}

async function testAllEnabledModels(chIdx, btn) {
    const ch = channelsData[chIdx];
    const enabledList = (ch.models || []).filter(m => m.enabled);
    if (enabledList.length === 0) {
        alert('请先勾选启用需要测速的模型！');
        return;
    }

    btn.disabled = true;
    btn.innerText = '⏳ 批量测速中...';

    for (const m of enabledList) {
        const modelId = m.id || m.ID;
        const statusKey = chIdx + '_' + modelId;
        modelStatusMap[statusKey] = { status: 'testing' };
    }
    document.getElementById('modelTableBody-' + chIdx).innerHTML = renderModelTableRows(chIdx, ch);

    const promises = enabledList.map(async (m) => {
        const modelId = m.id || m.ID;
        const statusKey = chIdx + '_' + modelId;
        try {
            const resp = await fetch('/api/test_model', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    provider: ch.provider,
                    api_key: ch.api_key,
                    base_url: ch.base_url,
                    model: modelId
                })
            });
            const res = await resp.json();
            if (res.success) {
                modelStatusMap[statusKey] = { status: 'ok', latency: res.latency_ms };
            } else {
                modelStatusMap[statusKey] = { status: 'err', error: res.error };
            }
        } catch (e) {
            modelStatusMap[statusKey] = { status: 'err', error: e.message };
        }
    });

    await Promise.all(promises);
    btn.disabled = false;
    btn.innerText = '⚡ 批量测速已启用模型';
    document.getElementById('modelTableBody-' + chIdx).innerHTML = renderModelTableRows(chIdx, ch);
}

function addChannel() {
    channelsData.push({
        id: 'ch_' + Date.now(),
        name: "新添加渠道",
        provider: "claude",
        api_key: "",
        base_url: "https://api.anthropic.com",
        models: [
            { id: "claude-3-7-sonnet-20250219", alias: "claude-3-7-sonnet", enabled: true, context_tokens: 200000, max_output_tokens: 8192, thinking_mode: "medium", thinking_budget: 4096 },
            { id: "claude-3-5-sonnet-20241022", alias: "claude-3-5-sonnet", enabled: true, context_tokens: 200000, max_output_tokens: 8192, thinking_mode: "off", thinking_budget: 0 }
        ]
    });
    renderChannels();
}

function removeChannel(idx) {
    channelsData.splice(idx, 1);
    renderChannels();
}

async function saveAllChannels() {
    try {
        const resp = await fetch('/api/config', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ channels: channelsData })
        });
        const res = await resp.json();
        if (res.success) {
            alert('✅ 配置保存成功！所有模型的上下文、输出上限与思考强度已即时生效。');
        } else {
            alert('❌ 保存失败: ' + res.error);
        }
    } catch (e) {
        alert('保存异常: ' + e.message);
    }
}

const sampleSets = {
    simple: {
        openai: {
            model: 'gpt-4o',
            messages: [
                { role: 'system', content: '你是一位资深的 Go 语言系统架构师。' },
                { role: 'user', content: '请用简洁的语言解释 Goroutine 与 OS 线程的区别。' }
            ],
            max_tokens: 2048,
            temperature: 0.7
        },
        claude: {
            model: 'claude-3-5-sonnet-20241022',
            system: [{ type: 'text', text: '你是一位资深的 Go 语言系统架构师。' }],
            messages: [
                { role: 'user', content: '请用简洁的语言解释 Goroutine 与 OS 线程的区别。' }
            ],
            max_tokens: 2048,
            temperature: 0.7
        },
        gemini: {
            contents: [
                { role: 'user', parts: [{ text: '请用简洁的语言解释 Goroutine 与 OS 线程的区别。' }] }
            ],
            systemInstruction: { parts: [{ text: '你是一位资深的 Go 语言系统架构师。' }] },
            generationConfig: { maxOutputTokens: 2048, temperature: 0.7 }
        },
        openai_responses: {
            model: 'gpt-4o',
            instructions: '你是一位资深的 Go 语言系统架构师。',
            input: [
                { role: 'user', content: '请用简洁的语言解释 Goroutine 与 OS 线程的区别。' }
            ],
            max_output_tokens: 2048,
            temperature: 0.7
        }
    },
    tools: {
        openai: {
            model: 'gpt-4o',
            messages: [{ role: 'user', content: '查询北京天气' }],
            tools: [{
                type: 'function',
                function: {
                    name: 'get_weather',
                    description: '查询天气',
                    parameters: { type: 'object', properties: { city: { type: 'string' } }, required: ['city'] }
                }
            }],
            max_tokens: 1024
        }
    }
};

function setConvSource(fmt, el) {
    curConvSource = fmt;
    el.parentElement.querySelectorAll('button').forEach(b => b.classList.remove('active'));
    el.classList.add('active');
    loadSample('simple');
}

function setConvTarget(fmt, el) {
    curConvTarget = fmt;
    el.parentElement.querySelectorAll('button').forEach(b => b.classList.remove('active'));
    el.classList.add('active');
    doConvert();
}

function loadSample(type) {
    const set = sampleSets[type] || sampleSets['simple'];
    const data = set[curConvSource] || set['openai'];
    document.getElementById('convInput').value = JSON.stringify(data, null, 2);
    doConvert();
}

async function doConvert() {
    const raw = document.getElementById('convInput').value.trim();
    if (!raw) return;
    let parsed;
    try { parsed = JSON.parse(raw); } catch (e) {
        document.getElementById('convOutput').innerText = '// JSON 格式错误: ' + e.message;
        return;
    }

    try {
        const resp = await fetch('/api/convert', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                source_format: curConvSource,
                target_format: curConvTarget,
                origin_model: parsed.model || 'gpt-4o',
                target_model: curConvTarget === 'claude' ? 'claude-3-5-sonnet' : (curConvTarget === 'gemini' ? 'gemini-1.5-pro' : 'gpt-4o'),
                payload: parsed
            })
        });
        const res = await resp.json();
        const outEl = document.getElementById('convOutput');
        const badgeEl = document.getElementById('qualityBadge');
        const stepsEl = document.getElementById('stepsText');

        if (!res.success) {
            badgeEl.style.display = 'none';
            outEl.innerText = '// 转换失败: ' + res.error;
            return;
        }

        badgeEl.style.display = 'inline-block';
        badgeEl.className = 'tag tag-' + res.quality;
        badgeEl.innerText = '等级: ' + res.quality.toUpperCase();
        stepsEl.innerText = res.steps && res.steps.length ? '步骤: ' + res.steps.map(s => s.Converter || s.From+'->'+s.To).join(' → ') : '';
        outEl.innerText = JSON.stringify(res.result, null, 2);
    } catch (e) {
        document.getElementById('convOutput').innerText = '// 异常: ' + e.message;
    }
}

function copyConvResult() {
    const txt = document.getElementById('convOutput').innerText;
    navigator.clipboard.writeText(txt).then(() => alert('已复制转换结果！'));
}

// 页面加载自动获取配置
fetchConfig();
