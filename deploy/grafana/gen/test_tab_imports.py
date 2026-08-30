"""Structural checks for the dashboard tab import registry."""

import ast
import unittest
from pathlib import Path


GEN_DIR = Path(__file__).resolve().parent
TABS_DIR = GEN_DIR / "tabs"
DASHBOARDS = GEN_DIR / "dashboards.py"


def _tab_modules():
    """Return concrete tab module names, excluding package/private helpers."""
    return {
        path.stem
        for path in TABS_DIR.glob("*.py")
        if path.name != "__init__.py" and not path.stem.startswith("_")
    }


def _imported_tab_modules(source):
    """Return modules imported directly by dashboards.py from tabs.*."""
    tree = ast.parse(source, filename=str(DASHBOARDS))
    imported = set()
    for node in ast.walk(tree):
        if not isinstance(node, ast.ImportFrom) or node.level != 0:
            continue
        if node.module and node.module.startswith("tabs."):
            imported.add(node.module.removeprefix("tabs."))
    return imported


def _orphaned_tab_modules(source):
    return sorted(_tab_modules() - _imported_tab_modules(source))


def assert_no_orphaned_tab_modules(source):
    orphaned = _orphaned_tab_modules(source)
    if orphaned:
        raise AssertionError(
            "tab modules are not imported by dashboards.py: %s"
            % ", ".join(orphaned)
        )


class TabImportTest(unittest.TestCase):
    def test_every_concrete_tab_module_is_imported(self):
        assert_no_orphaned_tab_modules(DASHBOARDS.read_text())

    def test_guard_rejects_a_removed_import(self):
        source = DASHBOARDS.read_text()
        imported = sorted(_imported_tab_modules(source))
        self.assertTrue(imported)
        module = imported[0]
        import_line = "from tabs.%s import " % module
        removed = next(line for line in source.splitlines(keepends=True)
                       if line.startswith(import_line))
        mutated = source.replace(removed, "", 1)

        with self.assertRaisesRegex(AssertionError, module):
            assert_no_orphaned_tab_modules(mutated)


if __name__ == "__main__":
    unittest.main()
