"""The operator's import PDF and row-result contracts."""
import unittest
from contract.build import build_spec, render_mcp

class OpeningImportFixPassContract(unittest.TestCase):
    def test_pdf_redirect_and_pinned_version_are_public(self):
        spec = build_spec()
        pdf = spec['paths']['/api/v1/invoices/{invoice_id}/pdf']['get']
        self.assertEqual(pdf['operationId'], 'getInvoicePdf')
        self.assertIn('application/pdf', pdf['responses']['200']['content'])
        redirect = pdf['responses']['303']
        self.assertTrue(redirect['headers']['Location']['required'])
        self.assertIn('pinned', pdf['description'])
        for schema in ('Invoice', 'InvoiceClient'):
            self.assertTrue(spec['components']['schemas'][schema]['properties']['document_of_record_version_id']['readOnly'])
        self.assertIn('getInvoicePdf', [t['name'] for t in render_mcp(spec)['tools']])

    def test_preview_and_commit_use_same_row_schema(self):
        spec = build_spec()
        op = spec['paths']['/api/v1/invoices:bulk']['post']
        expected = {'$ref': '#/components/schemas/BulkInvoiceResult'}
        for code in ('200', '201', '422'):
            schema = op['responses'][code]['content']['application/json']['schema']
            self.assertEqual(schema['properties']['results']['items'], expected)
        self.assertEqual(spec['components']['schemas']['BulkInvoiceResult']['properties']['row']['minimum'], 1)
