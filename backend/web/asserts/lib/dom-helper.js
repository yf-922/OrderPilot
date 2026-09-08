// ProductLib 是页面共享的小型前端工具库。
// 这样商品页和用户页都可以复用统一的 DOM 与请求方法。
window.ProductLib = {
    // setValue 根据元素 id 设置表单值。
    setValue: function (id, value) {
        var node = document.getElementById(id);
        if (node) {
            node.value = value || "";
        }
    },

    // setMessage 统一设置提示文本和样式类型。
    setMessage: function (id, text, type) {
        var node = document.getElementById(id);
        if (!node) {
            return;
        }

        node.textContent = text || "";
        node.className = "form-message " + (type || "");
    },

    // requestJSON 基于 fetch 发送请求，并假设返回体是 JSON。
    requestJSON: function (url, options) {
        return fetch(url, options).then(function (response) {
            return response.json();
        });
    }
};
