document.addEventListener("DOMContentLoaded", function () {
    var lib = window.ProductLib;
    var form = document.getElementById("order-create-form");
    if (!lib || !form) return;
    var body = document.getElementById("order-table-body");
    var productSelect = document.getElementById("order-product-id");
    var orderCount = document.getElementById("order-count");

    function tokenUserID() {
        var token = lib.getToken();
        if (!token) return "";
        try {
            var encoded = token.split(".")[0];
            encoded += "=".repeat((4 - encoded.length % 4) % 4);
            return atob(encoded.replace(/-/g, "+").replace(/_/g, "/")).split(".")[0];
        } catch (_) { return ""; }
    }
    function message(text, type) { lib.setMessage("order-create-message", text, type); }
    function statusLabel(value) { return value === 0 ? "待处理" : value === 1 ? "已确认" : "已取消"; }
    function addCell(row, value) { var cell = document.createElement("td"); cell.textContent = value; row.appendChild(cell); return cell; }
    function actionButton(text, handler, extraClass) {
        var button = document.createElement("button");
        button.type = "button";
        button.className = "action-btn " + (extraClass || "");
        button.textContent = text;
        button.addEventListener("click", handler);
        return button;
    }
    function renderOrders(items, total) {
        body.textContent = "";
        orderCount.textContent = "共 " + total + " 条订单";
        if (!items || !items.length) {
            var empty = document.createElement("tr");
            var cell = document.createElement("td");
            cell.colSpan = 5;
            cell.className = "empty-row";
            cell.textContent = "当前没有订单，可以从右侧创建。";
            empty.appendChild(cell);
            body.appendChild(empty);
            return;
        }
        items.forEach(function (order) {
            var row = document.createElement("tr");
            addCell(row, order.id);
            addCell(row, order.productId);
            addCell(row, order.quantity);
            addCell(row, statusLabel(order.orderStatus));
            var actions = document.createElement("td");
            actions.className = "nowrap-col";
            if (order.orderStatus === 0) {
                actions.appendChild(actionButton("确认", function () { updateOrder(order.id); }));
                actions.appendChild(actionButton("取消", function () { cancelOrder(order.id); }, "danger-btn"));
            } else actions.textContent = "无需操作";
            row.appendChild(actions);
            body.appendChild(row);
        });
    }
    function loadOrders() {
        if (!lib.getToken()) return;
        lib.requestJSON("/order/list?page=1&pageSize=50").then(function (result) {
            if (!result.success) { message(result.message || "订单加载失败", "error"); return; }
            renderOrders(result.data.items, result.data.total);
        }).catch(function () { message("订单加载失败，请检查服务状态。", "error"); });
    }
    function loadProducts() {
        if (!lib.getToken()) return;
        lib.requestJSON("/product/list?page=1&pageSize=100").then(function (result) {
            if (!result.success) { message(result.message || "商品加载失败", "error"); return; }
            productSelect.textContent = "";
            var placeholder = document.createElement("option");
            placeholder.value = "";
            placeholder.textContent = "请选择商品";
            productSelect.appendChild(placeholder);
            result.data.items.forEach(function (product) {
                var option = document.createElement("option");
                option.value = product.id;
                option.textContent = product.productName + "（库存 " + product.productNum + "）";
                option.disabled = product.productNum <= 0;
                productSelect.appendChild(option);
            });
        });
    }
    function updateOrder(id) {
        var request = new FormData();
        request.append("id", id);
        request.append("orderStatus", "1");
        lib.requestJSON("/order/update", {method: "POST", body: request}).then(function (result) {
            message(result.message || (result.success ? "订单已确认" : "订单确认失败"), result.success ? "success" : "error");
            if (result.success) loadOrders();
        });
    }
    function cancelOrder(id) {
        if (!window.confirm("确认取消订单 #" + id + " 吗？库存将自动恢复。")) return;
        var request = new FormData();
        request.append("id", id);
        lib.requestJSON("/order/delete", {method: "POST", body: request}).then(function (result) {
            message(result.message || (result.success ? "订单已取消" : "订单取消失败"), result.success ? "success" : "error");
            if (result.success) { loadOrders(); loadProducts(); }
        });
    }
    form.addEventListener("submit", function (event) {
        event.preventDefault();
        var userID = tokenUserID();
        if (!userID) { message("请先在用户页登录。", "error"); return; }
        document.getElementById("order-user-id").value = userID;
        var headers = {"Idempotency-Key": "web-" + Date.now() + "-" + Math.random().toString(36).slice(2, 10)};
        lib.requestJSON(form.action, {method: "POST", headers: headers, body: new FormData(form)}).then(function (result) {
            message(result.message || (result.success ? "订单创建成功" : "订单创建失败"), result.success ? "success" : "error");
            if (result.success) { form.reset(); loadOrders(); loadProducts(); }
        });
    });
    document.getElementById("order-refresh").addEventListener("click", function () { loadOrders(); loadProducts(); });
    if (!lib.getToken()) message("请先在用户页面登录后再创建或查看订单。", "error");
    else { loadOrders(); loadProducts(); }
});
