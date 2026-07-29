.PHONY: build check test package-example-check image clean

build:
	$(MAKE) -C J-agent build
	$(MAKE) -C J-agent/research/jspace build
	$(MAKE) -C J-mcp build
	$(MAKE) -C J-mem build
	$(MAKE) -C J-packages build
	$(MAKE) -C J-skills build
	$(MAKE) -C J-subagents build
	$(MAKE) -C J-tui build

check:
	$(MAKE) -C J-agent check
	$(MAKE) -C J-agent/research/jspace check
	$(MAKE) -C J-mcp check
	$(MAKE) -C J-mem check
	$(MAKE) -C J-packages check
	$(MAKE) -C J-skills check
	$(MAKE) -C J-subagents check
	$(MAKE) -C J-tui check
	$(MAKE) package-example-check

test:
	$(MAKE) -C J-agent test
	$(MAKE) -C J-agent/research/jspace test
	$(MAKE) -C J-mcp test
	$(MAKE) -C J-mem test
	$(MAKE) -C J-packages test
	$(MAKE) -C J-skills test
	$(MAKE) -C J-subagents test
	$(MAKE) -C J-tui test
	$(MAKE) package-example-check

package-example-check:
	cd examples/packages/j-hermes-memory && python3 -W error::ResourceWarning -m unittest -v test_server.py

image:
	docker build --tag j:dev .

clean:
	$(MAKE) -C J-agent clean
	$(MAKE) -C J-agent/research/jspace clean
	$(MAKE) -C J-packages clean
	$(MAKE) -C J-tui clean
