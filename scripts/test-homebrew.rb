# ABOUTME: Installs a real local Judgement snapshot through a disposable Homebrew tap.
# ABOUTME: Preserves existing installs and removes only this test's tap and package.
require 'open3'

if ARGV == ['--help'] || ARGV == ['-h']
  puts 'Usage: ruby scripts/test-homebrew.rb'
  puts 'Install/test the generated dist/judgement.rb using local snapshot archives, then clean up.'
  exit
end
abort 'Unexpected argument; use --help' unless ARGV.empty?
Dir.chdir(File.expand_path('..', __dir__))

ENV['HOMEBREW_NO_AUTO_UPDATE'] = '1'
ENV['HOMEBREW_NO_INSTALL_CLEANUP'] = '1'

def run(*args)
  raise "Command failed: #{args.first}" unless system(*args)
end

def judgement_installed?
  existing, status = Open3.capture2('brew', 'list', '--formula', '--versions')
  raise 'Cannot inspect existing Homebrew packages' unless status.success?
  existing.lines.any? { |line| line.start_with?('judgement ') }
end

abort 'An existing judgement install must be preserved; smoke test canceled' if judgement_installed?
tap = "judgement-validation/snapshot-#{Process.pid}"
created = false
begin
  run('brew', 'tap-new', '--no-git', tap)
  created = true
  directory, status = Open3.capture2('brew', '--repository', tap)
  raise 'Cannot find temporary tap' unless status.success?
  formula = File.read('dist/judgement.rb')
  formula = formula.gsub(%r{https://github.com/2389-research/judgement/releases/download/[^/]+/}, "file://#{File.expand_path('dist')}/")
  File.write(File.join(directory.strip, 'Formula', 'judgement.rb'), formula)
  run('brew', 'install', '--formula', "#{tap}/judgement")
  run('brew', 'test', "#{tap}/judgement")
ensure
  begin
    run('brew', 'uninstall', '--formula', "#{tap}/judgement") if created && judgement_installed?
  ensure
    run('brew', 'untap', tap) if created
  end
end
