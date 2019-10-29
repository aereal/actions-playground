import { CloudAssembly } from "@aws-cdk/cx-api";
import { app } from "../src/app";

describe("App", () => {
  let assembly: CloudAssembly;

  beforeAll(() => {
    assembly = app.synth();
  });

  test("parameters", () => {
    expect(assembly.stacks.map(stack => stack.parameters)).toMatchSnapshot();
  });

  test("templates", () => {
    expect(assembly.stacks.map(stack => stack.template)).toMatchSnapshot();
  });
});
