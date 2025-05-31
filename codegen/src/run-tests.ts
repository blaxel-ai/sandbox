import { BlToolsTestSuite } from './test-cases.js';

async function main() {
  const isProduction = process.env.NODE_ENV === 'production';
  const mode = isProduction ? 'production (sandbox)' : 'development (local)';

  console.log(`🧪 Starting MCP BlTools Test Suite in ${mode} mode...\n`);

  const testSuite = new BlToolsTestSuite();
  await testSuite.runAllTests();
}

main()
  .catch((err) => {
    console.error("There was an error => ", err);
    process.exit(1);
  })
  .then(() => {
    console.log('\n🎉 Test suite completed!');
    process.exit(0);
  });