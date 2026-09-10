document.addEventListener("DOMContentLoaded", function () {
    var form = document.getElementById("agent-form");
    if (!form) return;

    var lib = window.ProductLib;
    var input = document.getElementById("agent-input");
    var messages = document.getElementById("agent-messages");
    var traces = document.getElementById("agent-traces");
    var send = document.getElementById("agent-send");
    var conversationID = "";

    function addMessage(role, text) {
        var item = document.createElement("article");
        item.className = "chat-message " + role;
        var label = document.createElement("span");
        label.className = "chat-role";
        label.textContent = role === "user" ? "你" : "客服 Agent";
        var content = document.createElement("p");
        content.textContent = text;
        item.appendChild(label);
        item.appendChild(content);
        messages.appendChild(item);
        messages.scrollTop = messages.scrollHeight;
        return item;
    }

    function showTraces(items, usage) {
        items = items || [];
        var duration = items.reduce(function (total, item) { return total + (item.durationMs || 0); }, 0);
        document.getElementById("agent-tool-count").textContent = String(items.length);
        document.getElementById("agent-tool-duration").textContent = duration + " ms";
        document.getElementById("agent-token-count").textContent = String(usage && usage.totalTokens || 0);
        if (!items || !items.length) return;
        traces.textContent = "";
        items.forEach(function (trace) {
            var item = document.createElement("section");
            item.className = "trace-item";
            var head = document.createElement("div");
            head.className = "trace-head";
            var name = document.createElement("strong");
            name.textContent = trace.name;
            var duration = document.createElement("span");
            duration.textContent = trace.durationMs + " ms";
            head.appendChild(name);
            head.appendChild(duration);
            var body = document.createElement("pre");
            body.textContent = JSON.stringify({arguments: trace.arguments, result: trace.result}, null, 2);
            item.appendChild(head);
            item.appendChild(body);
            traces.appendChild(item);
        });
    }

    function addConfirmation(confirmation) {
        if (!confirmation) return;
        var box = document.createElement("div");
        box.className = "confirmation-bar";
        var text = document.createElement("span");
        text.textContent = "确认取消订单 " + confirmation.orderId + "？";
        var button = document.createElement("button");
        button.className = "danger-confirm-btn";
        button.type = "button";
        button.textContent = "确认取消";
        button.addEventListener("click", function () {
            button.disabled = true;
            lib.requestJSON("/agent/confirm", {
                method: "POST",
                headers: {"Content-Type": "application/json"},
                body: JSON.stringify({conversationId: conversationID, token: confirmation.token})
            }).then(function (result) {
                if (!result.success) throw new Error(result.message || "操作失败");
                box.remove();
                addMessage("assistant", result.data.message);
                showTraces(result.data.toolCalls, result.data.usage);
            }).catch(function (error) {
                button.disabled = false;
                text.textContent = error.message;
            });
        });
        box.appendChild(text);
        box.appendChild(button);
        messages.appendChild(box);
        messages.scrollTop = messages.scrollHeight;
    }

    function sendMessage(text) {
        if (!lib.getToken()) {
            addMessage("assistant", "请先在用户页登录，再使用客服 Agent。");
            return;
        }
        addMessage("user", text);
        var loading = addMessage("assistant", "正在分析并调用业务工具...");
        send.disabled = true;
        lib.requestJSON("/agent/chat", {
            method: "POST",
            headers: {"Content-Type": "application/json"},
            body: JSON.stringify({conversationId: conversationID, message: text})
        }).then(function (result) {
            loading.remove();
            if (!result.success) throw new Error(result.message || "Agent 请求失败");
            conversationID = result.data.conversationId;
            addMessage("assistant", result.data.message);
            showTraces(result.data.toolCalls, result.data.usage);
            addConfirmation(result.data.confirmation);
        }).catch(function (error) {
            loading.remove();
            addMessage("assistant", error.message);
        }).finally(function () {
            send.disabled = false;
            input.focus();
        });
    }

    form.addEventListener("submit", function (event) {
        event.preventDefault();
        var text = input.value.trim();
        if (!text) return;
        input.value = "";
        sendMessage(text);
    });

    document.querySelectorAll("[data-prompt]").forEach(function (button) {
        button.addEventListener("click", function () { sendMessage(button.dataset.prompt); });
    });

    document.getElementById("agent-reset").addEventListener("click", function () {
        var oldID = conversationID;
        conversationID = "";
        messages.innerHTML = '<article class="chat-message assistant"><span class="chat-role">客服 Agent</span><p>新会话已开始。需要我查询订单还是商品库存？</p></article>';
        traces.innerHTML = '<p class="muted-text">Agent 调用业务工具后，将在这里展示参数、结果与耗时。</p>';
        showTraces([], {});
        if (oldID) lib.requestJSON("/agent/reset", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({conversationId: oldID})});
    });

    if (!lib.getToken()) {
        document.getElementById("agent-mode").textContent = "需要登录";
        document.getElementById("agent-model").textContent = "";
        return;
    }
    lib.requestJSON("/agent/status").then(function (result) {
        if (!result.success) return;
        document.getElementById("agent-mode").textContent = result.data.mode === "live" ? "Grok 在线" : "本地演示";
        document.getElementById("agent-model").textContent = result.data.model;
    });
});
