// Bulk-delete conveniences: select all and a final confirmation. The form
// still works without JavaScript; the server validates the selection and
// CSRF token either way.
const form = document.querySelector("[data-delete-form]");

if (form) {
  const boxes = Array.from(form.querySelectorAll('input[name="book_id"]'));
  const selectAll = form.querySelector("[data-select-all]");

  function updateSelectAll() {
    if (!selectAll) {
      return;
    }
    const checked = boxes.filter((box) => box.checked).length;
    selectAll.checked = boxes.length > 0 && checked === boxes.length;
    selectAll.indeterminate = checked > 0 && checked < boxes.length;
  }

  if (selectAll) {
    selectAll.addEventListener("change", () => {
      for (const box of boxes) {
        box.checked = selectAll.checked;
      }
      updateSelectAll();
    });
    for (const box of boxes) {
      box.addEventListener("change", updateSelectAll);
    }
  }

  form.addEventListener("submit", (event) => {
    if (!boxes.some((box) => box.checked)) {
      event.preventDefault();
      window.alert("Select at least one book to delete.");
      return;
    }
    if (!window.confirm("Delete the selected books? This cannot be undone.")) {
      event.preventDefault();
    }
  });
}
