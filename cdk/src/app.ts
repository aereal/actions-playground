import { App } from "@aws-cdk/core";
import { DBStack } from "./stacks/db";
import { NetworkStack } from "./stacks/network";

export const app = new App();
const { defaultVPC } = new NetworkStack(app, "network", {});
new DBStack(app, "db", { vpc: defaultVPC });
app.synth();
