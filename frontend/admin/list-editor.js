export function renderListItem(value, inputClass, placeholder, ariaLabel) {
  const row = document.createElement("div");
  row.className = "list-editor-item";

  const input = document.createElement("input");
  input.className = inputClass;
  input.name = inputClass;
  input.value = value;
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = placeholder;
  input.setAttribute("aria-label", ariaLabel);

  const remove = document.createElement("button");
  remove.className =
    "secondary compact icon-only trash-button list-editor-remove";
  remove.type = "button";
  remove.title = "Remove";
  remove.setAttribute("aria-label", "Remove");
  remove.append(document.createElement("span"));
  remove.addEventListener("click", () => {
    const list = row.parentElement;
    row.remove();
    updateListRemoveButtons(list);
  });

  row.append(input, remove);
  return row;
}

export function updateListRemoveButtons(container) {
  const buttons = container.querySelectorAll(".list-editor-remove");
  buttons.forEach((button) => {
    button.disabled = buttons.length <= 1;
  });
}

export function listEditor(title, inputClass, values, placeholder) {
  const editor = document.createElement("div");
  editor.className = "list-editor room-list-editor";

  const head = document.createElement("div");
  head.className = "list-editor-head";
  const label = document.createElement("span");
  label.textContent = title;
  const add = document.createElement("button");
  add.className = "secondary compact";
  add.type = "button";
  add.textContent = "Add";
  head.append(label, add);

  const list = document.createElement("div");
  list.className = "list-editor-items";
  const rows = values.length > 0 ? values : [""];
  list.replaceChildren(
    ...rows.map((value) =>
      renderListItem(value, inputClass, placeholder, title),
    ),
  );

  add.addEventListener("click", () => {
    const row = renderListItem("", inputClass, placeholder, title);
    list.append(row);
    updateListRemoveButtons(list);
    row.querySelector(`.${inputClass}`).focus();
  });

  editor.append(head, list);
  updateListRemoveButtons(list);
  return editor;
}
