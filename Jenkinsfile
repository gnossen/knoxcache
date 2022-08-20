@Library("artifact-store-library") _

pipeline {
    agent any

    post {
        always {
            deleteDir()
        }
    }

    options {
        disableConcurrentBuilds()
    }

    stages {
        stage('Build Browser Extensions') {
            steps {
                // TODO: Only when there are changes to it.
                sh "docker pull docker:5000/build-image:latest"
                sh "docker run -v `pwd`:/build -e \"BUILD_NUMBER=${BUILD_NUMBER}\" docker:5000/build-image:latest bash -c 'cd /build/chromium-extension && make clean && make'"
                sh "docker run -v `pwd`:/build -e \"BUILD_NUMBER=${BUILD_NUMBER}\" docker:5000/build-image:latest bash -c 'cd /build/firefox-addon && make clean && make'"
            }
        }

        stage('Publish Browser Extensions') {
            // TODO: Only when there are changes to it.
            when { branch "main" }
            environment {
                ART_STORE = credentials("artifact-store")
            }
            steps {
                script {
                    chromiumVersion = sh(script: "cd chromium-extension; make print-version", returnStdout: true).trim()
                    chromiumArtifactName = "knox-chromium-extension-${chromiumVersion}.zip"
                    chromiumRemoteDirectory = "knox/chromium-extension"
                    uploadArtifact("chromium-extension/${chromiumArtifactName}", chromiumRemoteDirectory, env.ART_STORE_USR, env.ART_STORE_PSW)
                    updateLink("knox/chromium-extension", chromiumArtifactName, "knox-chromium-extension-latest.zip", env.ART_STORE_USR, env.ART_STORE_PSW)

                    firefoxVersion = sh(script: "cd firefox-addon; make print-version", returnStdout: true).trim()
                    firefoxArtifactName = "knox-firefox-addon-${firefoxVersion}.xpi"
                    firefoxRemoteDirectory = "knox/firefox-addon"
                    uploadArtifact("firefox-addon/${firefoxArtifactName}", firefoxRemoteDirectory, env.ART_STORE_USR, env.ART_STORE_PSW)
                    updateLink("knox/firefox-addon", firefoxArtifactName, "knox-firefox-addon-latest.xpi", env.ART_STORE_USR, env.ART_STORE_PSW)
                }
            }
        }
    }
}
