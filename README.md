# bg-change-stack

cf plugin for zero downtime application stack change, highly inspired by [cf-plugin-bg-restage](https://github.com/CAFxX/cf-plugin-bg-restage) & [stack auditor](https://github.com/cloudfoundry/stack-auditor).

Use this plugin when your CF environment doesn't support ZDT restart. 
The `stack auditor` plugin does a restart with down time, when the CAPI version doesn't support ZDT endpoint. 
This plugin attempts to support such CF environments by leveraging `bg-restage` combined with stack change logics. 

## Installation

```
git clone https://github.com/naridnevahgar/cf-bg-change-stack.git
cd cf-bg-change-stack
./bin/build.sh
./bin/install_plugin.sh
```

## Usage

```
$ cf bg-change-stack <app name> <new stack name>
```

## Method

1. It retrieves manifest from old app in a directory and create fake file as content to be pushed.

2. The old application is renamed to `<APP-NAME>-venerable`. It keeps its old route
   mappings and this change is invisible to users.

3. The new application is pushed to `<APP-NAME>`, this push will normally failed because we just want to create an app
   but not push real code (we do that because there is no easy way to create an app without pushing code as a cli plugin). 
   **Note**: you will not see any failures and if it's not failed the app will not be started.

4. Bits will be copied from old app to the new app to put real code inside the new app.

5. The new app will be restarted which will restage the app with the real code from old app.

6. The new app's stack will be changed using the `/v3/apps` endpoint.

7. The new app will be restarted again for the new stack to take effect.

6. The old app will be removed and all traffic will be on the new app.
