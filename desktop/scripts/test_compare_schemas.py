import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("compare_schemas", Path(__file__).with_name("compare-schemas.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SchemaCompatibilityTest(unittest.TestCase):
    def test_current_stable_export(self):
        root = Path(__file__).resolve().parents[1] / "testdata/app-server-schema"
        report = module.compare(root/"0.159.2", root/"0.165.0-alpha.1.2/stable")
        self.assertEqual(report["methods"]["ClientRequest.json"]["added"], ["feedback/save", "thread/attachmentOwner/list"])
        self.assertEqual(report["methods"]["ClientRequest.json"]["removed"], [])
        changes = report["changes"]
        self.assertTrue(any(c["kind"] == "field_removed" and "imageGeneration/properties/result" in c["path"] for c in changes))
        self.assertTrue(any(c["kind"] == "required_changed" and "namespaceTools" in c["removed"] for c in changes))
        self.assertTrue(any("partial_answer" in c["path"] and c["kind"] == "added" for c in changes))

    def test_documentation_and_set_order_are_not_wire_changes(self):
        self.assertEqual(list(module.changes({"description":"old", "required":["b","a"], "enum":[1,2]}, {"description":"new", "required":["a","b"], "enum":[2,1]})), [])


if __name__ == "__main__":
    unittest.main()
